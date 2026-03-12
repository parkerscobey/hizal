package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/XferOps/winnow/internal/auth"
	"github.com/XferOps/winnow/internal/models"
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

	// Resolve agent's org_id for denormalized storage on the key row.
	var agent models.Agent
	if err := h.pool.QueryRow(r.Context(),
		`SELECT id, org_id, slug FROM agents WHERE id = $1`, agentID,
	).Scan(&agent.ID, &agent.OrgID, &agent.Slug); err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to resolve agent org")
		return
	}

	plaintext, keyHash, err := auth.GenerateAPIKey(agent.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KEYGEN_FAILED", err.Error())
		return
	}

	var key models.APIKey
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO api_keys (owner_type, agent_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ('AGENT', $1, $2, $3, $4, FALSE, '{}')
		RETURNING id
	`, agentID, agent.OrgID, keyHash, body.Name).Scan(&key.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       key.ID,
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
		var key models.APIKey
		if err := rows.Scan(&key.ID, &key.Name, &key.CreatedAt, &key.LastUsedAt); err != nil {
			continue
		}
		k := keyItem{
			ID:        key.ID,
			Name:      key.Name,
			CreatedAt: key.CreatedAt.Format(time.RFC3339),
		}
		if key.LastUsedAt != nil {
			s := key.LastUsedAt.Format(time.RFC3339)
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
