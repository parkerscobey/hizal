package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/XferOps/hizal/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

type authError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]authError{
		"error": {Code: code, Message: msg},
	})
}

// APIKeyAuth returns a middleware that validates Bearer tokens against api_keys table.
func APIKeyAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if pool == nil {
				writeAuthError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeAuthError(w, http.StatusUnauthorized, "AUTH_INVALID", "missing or invalid Authorization header")
				return
			}
			plaintext := strings.TrimPrefix(authHeader, "Bearer ")
			keyHash := auth.HashKey(plaintext)

			// Look up the key — org_id is denormalized so no JOIN needed.
			row := pool.QueryRow(r.Context(), `
				SELECT id, org_id, agent_id, scope_all_projects, allowed_project_ids
				FROM api_keys
				WHERE key_hash = $1
			`, keyHash)

			var (
				keyID      string
				orgID      string
				agentID    *string
				scopeAll   bool
				allowedIDs []string
			)
			if err := row.Scan(&keyID, &orgID, &agentID, &scopeAll, &allowedIDs); err != nil {
				writeAuthError(w, http.StatusUnauthorized, "AUTH_INVALID", "invalid API key")
				return
			}
			authAgentID := ""
			if agentID != nil {
				authAgentID = *agentID
			}

			// Resolve project_id
			projectID := ""
			if !scopeAll && len(allowedIDs) == 1 {
				projectID = allowedIDs[0]
			}
			// If multi-project or all-projects scope, check X-Project-ID header
			if projectID == "" {
				projectID = r.Header.Get("X-Project-ID")
			}

			// Update last_used_at async
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = pool.Exec(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, keyID)
			}()

			claims := AuthClaims{
				OrgID:            orgID,
				ProjectID:        projectID,
				KeyID:            keyID,
				AgentID:          authAgentID,
				ScopeAllProjects: scopeAll,
				AllowedProjects:  allowedIDs,
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// SkillAuth accepts either a JWT or an API key for skill catalog routes.
func SkillAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	apiKeyAuth := APIKeyAuth(pool)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeAuthError(w, http.StatusUnauthorized, "AUTH_INVALID", "missing or invalid Authorization header")
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			jwtClaims, err := ParseJWT(tokenStr)
			if err == nil {
				user := JWTUser{ID: jwtClaims.UserID, Email: jwtClaims.Email}
				next.ServeHTTP(w, r.WithContext(withJWTUser(r.Context(), user)))
				return
			}

			apiKeyAuth(next).ServeHTTP(w, r)
		})
	}
}

// ContextAuth accepts either a JWT or an API key for context routes.
// JWT callers must scope the request to a project via `project_id` or `X-Project-ID`.
func ContextAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	apiKeyAuth := APIKeyAuth(pool)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if pool == nil {
				writeAuthError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeAuthError(w, http.StatusUnauthorized, "AUTH_INVALID", "missing or invalid Authorization header")
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			jwtClaims, err := ParseJWT(tokenStr)
			if err != nil {
				apiKeyAuth(next).ServeHTTP(w, r)
				return
			}

			projectID := r.URL.Query().Get("project_id")
			if projectID == "" {
				projectID = r.Header.Get("X-Project-ID")
			}

			user := JWTUser{ID: jwtClaims.UserID, Email: jwtClaims.Email}
			ctx := withJWTUser(r.Context(), user)
			scopedReq := r.WithContext(ctx)

			// Agent-scoped or org-scoped requests may not have a project_id.
			// Resolve org from the agent, org param, or chunk ID instead.
			if projectID == "" {
				agentID := r.URL.Query().Get("agent_id")
				orgID := r.URL.Query().Get("org_id")

				if agentID != "" {
					// Resolve org from agent
					err := pool.QueryRow(r.Context(),
						`SELECT org_id FROM agents WHERE id = $1`, agentID,
					).Scan(&orgID)
					if err != nil {
						writeAuthError(w, http.StatusBadRequest, "INVALID_AGENT", "agent not found")
						return
					}
				}

				// If no agent_id or org_id, try resolving from a chunk ID in the URL path.
				// This covers GET/PATCH/DELETE /v1/context/:id and GET /v1/context/:id/versions.
				// Also covers GET /v1/context/:id/reviews (HIZAL-137)
				if orgID == "" {
					chunkID := extractChunkIDFromPath(r.URL.Path)
					if chunkID != "" {
						_ = pool.QueryRow(r.Context(), `
							SELECT COALESCE(
								cc.org_id,
								(SELECT p.org_id FROM projects p WHERE p.id = cc.project_id),
								(SELECT a.org_id FROM agents a WHERE a.id = cc.agent_id)
							)
							FROM context_chunks cc WHERE cc.id = $1
						`, chunkID).Scan(&orgID)
					}
				}

				if orgID != "" {
					// Verify caller is a member of this org
					if _, err := requireOrgRole(scopedReq, pool, orgID, "owner", "admin", "member", "viewer"); err != nil {
						writeAuthError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
						return
					}
					claims := AuthClaims{OrgID: orgID}
					next.ServeHTTP(w, scopedReq.WithContext(withClaims(ctx, claims)))
					return
				}

				writeAuthError(w, http.StatusBadRequest, "PROJECT_REQUIRED", "project_id, agent_id, or org_id is required")
				return
			}

			_, orgID, err := requireProjectAccess(scopedReq, pool, projectID)
			if err != nil {
				switch err.Error() {
				case "project not found", "not a member of this project", "insufficient permissions":
					writeAuthError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
				default:
					writeAuthError(w, http.StatusInternalServerError, "DB_ERROR", "failed to authorize project access")
				}
				return
			}

			claims := AuthClaims{
				OrgID:     orgID,
				ProjectID: projectID,
			}
			next.ServeHTTP(w, scopedReq.WithContext(withClaims(ctx, claims)))
		})
	}
}

func extractChunkIDFromPath(urlPath string) string {
	// Path format: /v1/context/{chunkID} or /v1/context/{chunkID}/reviews or /v1/context/{chunkID}/versions
	// We want the segment after /v1/context/
	segments := strings.Split(strings.TrimPrefix(urlPath, "/v1/context/"), "/")
	if len(segments) > 0 && isValidUUID(segments[0]) {
		return segments[0]
	}
	return ""
}

func isValidUUID(s string) bool {
	// Simple UUID check: 8-4-4-4-12 hex format
	if len(s) != 36 {
		return false
	}
	// Check dashes at correct positions
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	// Check all other chars are hex
	hexChars := "0123456789abcdefABCDEF-"
	for _, c := range s {
		if !strings.ContainsRune(hexChars, c) {
			return false
		}
	}
	return true
}

func PlatformAdminOnly() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := JWTUserFrom(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required")
				return
			}
			adminIDs := parseAdminIDs(os.Getenv("ADMIN_IDS"))
			for _, id := range adminIDs {
				if id == user.ID {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeError(w, http.StatusForbidden, "FORBIDDEN", "platform admin access required")
		})
	}
}

func parseAdminIDs(raw string) []string {
	var ids []string
	for _, s := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(s); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
