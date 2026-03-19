package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/XferOps/winnow/internal/auth"
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
				SELECT id, org_id, scope_all_projects, allowed_project_ids
				FROM api_keys
				WHERE key_hash = $1
			`, keyHash)

			var (
				keyID      string
				orgID      string
				scopeAll   bool
				allowedIDs []string
			)
			if err := row.Scan(&keyID, &orgID, &scopeAll, &allowedIDs); err != nil {
				writeAuthError(w, http.StatusUnauthorized, "AUTH_INVALID", "invalid API key")
				return
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
			// Resolve org from the agent or org param instead.
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
