package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/XferOps/winnow/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentKeyHandlers struct {
	pool *pgxpool.Pool
}

func NewAgentKeyHandlers(pool *pgxpool.Pool) *AgentKeyHandlers {
	return &AgentKeyHandlers{pool: pool}
}

// POST /v1/agents/:id/keys
func (h *AgentKeyHandlers) CreateAgentKey(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name is required")
		return
	}

	plaintext, keyHash, err := auth.GenerateAPIKey(body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KEYGEN_FAILED", err.Error())
		return
	}

	var keyID string
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO api_keys (owner_type, agent_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ('AGENT', $1, $2, $3, FALSE, '{}')
		RETURNING id
	`, agentID, keyHash, body.Name).Scan(&keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       keyID,
		"key":      plaintext,
		"name":     body.Name,
		"agent_id": agentID,
		"note":     "Store this key securely — it will not be shown again.",
	})
}

// GET /v1/agents/:id/keys
func (h *AgentKeyHandlers) ListAgentKeys(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin", "member", "viewer"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, created_at, last_used_at
		FROM api_keys
		WHERE agent_id = $1 AND owner_type = 'AGENT'
		ORDER BY created_at DESC
	`, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type keyItem struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		CreatedAt  string  `json:"created_at"`
		LastUsedAt *string `json:"last_used_at"`
	}
	var keys []keyItem
	for rows.Next() {
		var k keyItem
		var createdAt time.Time
		var lastUsed *time.Time
		if err := rows.Scan(&k.ID, &k.Name, &createdAt, &lastUsed); err != nil {
			continue
		}
		k.CreatedAt = createdAt.Format(time.RFC3339)
		if lastUsed != nil {
			s := lastUsed.Format(time.RFC3339)
			k.LastUsedAt = &s
		}
		keys = append(keys, k)
	}
	if keys == nil {
		keys = []keyItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})
}

// DELETE /v1/agents/:id/keys/:keyId
func (h *AgentKeyHandlers) DeleteAgentKey(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	keyID := chi.URLParam(r, "keyId")

	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM api_keys WHERE id = $1 AND agent_id = $2 AND owner_type = 'AGENT'`,
		keyID, agentID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "key not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
