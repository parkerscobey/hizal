package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/XferOps/contextor/internal/auth"
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

			// Look up the key + get org via user membership
			row := pool.QueryRow(r.Context(), `
				SELECT ak.id, ak.scope_all_projects, ak.allowed_project_ids, om.org_id
				FROM api_keys ak
				JOIN users u ON u.id = ak.user_id
				JOIN org_memberships om ON om.user_id = u.id
				WHERE ak.key_hash = $1
			`, keyHash)

			var (
				keyID            string
				scopeAll         bool
				allowedIDs       []string
				orgID            string
			)
			if err := row.Scan(&keyID, &scopeAll, &allowedIDs, &orgID); err != nil {
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
