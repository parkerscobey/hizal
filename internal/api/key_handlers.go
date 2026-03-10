package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/XferOps/winnow/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type KeyHandlers struct {
	pool *pgxpool.Pool
}

func NewKeyHandlers(pool *pgxpool.Pool) *KeyHandlers {
	return &KeyHandlers{pool: pool}
}

// POST /v1/keys (JWT auth)
func (h *KeyHandlers) CreateKey(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	var body struct {
		Name       string   `json:"name"`
		OrgID      string   `json:"org_id"`
		ProjectIDs []string `json:"project_ids"`
		ScopeAll   bool     `json:"scope_all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if body.Name == "" || body.OrgID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "name and org_id are required")
		return
	}

	// Verify caller is member of org
	if _, err := requireOrgRole(r, h.pool, body.OrgID, "owner", "admin", "member"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	projectIDs := body.ProjectIDs
	if projectIDs == nil {
		projectIDs = []string{}
	}

	plaintext, keyHash, err := auth.GenerateAPIKey(body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KEYGEN_FAILED", err.Error())
		return
	}

	var keyID string
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO api_keys (owner_type, user_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ('USER', $1, $2, $3, $4, $5)
		RETURNING id
	`, user.ID, keyHash, body.Name, body.ScopeAll, projectIDs).Scan(&keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":   keyID,
		"key":  plaintext,
		"name": body.Name,
		"note": "Store this key securely — it will not be shown again.",
	})
}

// GET /v1/keys — list keys for current user (masked)
func (h *KeyHandlers) ListKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, scope_all_projects, allowed_project_ids, created_at, last_used_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type keyItem struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		ScopeAll    bool     `json:"scope_all_projects"`
		ProjectIDs  []string `json:"allowed_project_ids"`
		CreatedAt   string   `json:"created_at"`
		LastUsedAt  *string  `json:"last_used_at"`
	}
	var keys []keyItem
	for rows.Next() {
		var k keyItem
		var createdAt time.Time
		var lastUsed *time.Time
		if err := rows.Scan(&k.ID, &k.Name, &k.ScopeAll, &k.ProjectIDs, &createdAt, &lastUsed); err != nil {
			continue
		}
		k.CreatedAt = createdAt.Format(time.RFC3339)
		if lastUsed != nil {
			formatted := lastUsed.Format(time.RFC3339)
			k.LastUsedAt = &formatted
		}
		keys = append(keys, k)
	}
	if keys == nil {
		keys = []keyItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})
}

// DELETE /v1/keys/:id
func (h *KeyHandlers) DeleteKey(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}
	keyID := chi.URLParam(r, "id")

	tag, err := h.pool.Exec(r.Context(), `
		DELETE FROM api_keys WHERE id = $1 AND user_id = $2
	`, keyID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "key not found or not yours")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
