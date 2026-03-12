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

	var org models.Org
	err := h.pool.QueryRow(r.Context(), `SELECT id, slug FROM orgs WHERE id = $1`, body.OrgID).Scan(&org.ID, &org.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to resolve org slug")
		return
	}

	plaintext, keyHash, err := auth.GenerateAPIKey(org.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KEYGEN_FAILED", err.Error())
		return
	}

	var key models.APIKey
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO api_keys (owner_type, user_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ('USER', $1, $2, $3, $4, $5, $6)
		RETURNING id
	`, user.ID, body.OrgID, keyHash, body.Name, body.ScopeAll, projectIDs).Scan(&key.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":   key.ID,
		"key":  plaintext,
		"name": body.Name,
		"note": "Store this key securely — it will not be shown again.",
	})
}

// GET /v1/keys — list keys visible to the current user across their orgs.
func (h *KeyHandlers) ListKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT DISTINCT
			ak.id,
			ak.org_id,
			ak.owner_type,
			ak.user_id,
			u.name,
			ak.agent_id,
			a.name,
			a.slug,
			ak.name,
			ak.scope_all_projects,
			ak.allowed_project_ids,
			CASE
				WHEN ak.owner_type = 'AGENT' THEN COALESCE(agent_project_names.project_names, ARRAY[]::text[])
				WHEN ak.scope_all_projects THEN COALESCE(org_project_names.project_names, ARRAY[]::text[])
				ELSE COALESCE(allowed_project_names.project_names, ARRAY[]::text[])
			END AS allowed_project_names,
			ak.created_at,
			ak.last_used_at
		FROM api_keys ak
		JOIN org_memberships om ON om.org_id = ak.org_id
		LEFT JOIN users u ON u.id = ak.user_id
		LEFT JOIN agents a ON a.id = ak.agent_id
		LEFT JOIN LATERAL (
			SELECT array_agg(p.name ORDER BY p.created_at) AS project_names
			FROM projects p
			WHERE p.org_id = ak.org_id
		) org_project_names ON TRUE
		LEFT JOIN LATERAL (
			SELECT array_agg(p.name ORDER BY p.created_at) AS project_names
			FROM projects p
			WHERE p.id = ANY(COALESCE(ak.allowed_project_ids, '{}'::uuid[]))
		) allowed_project_names ON TRUE
		LEFT JOIN LATERAL (
			SELECT array_agg(p.name ORDER BY p.created_at) AS project_names
			FROM projects p
			JOIN agent_projects ap ON ap.project_id = p.id
			WHERE ap.agent_id = ak.agent_id
		) agent_project_names ON TRUE
		WHERE om.user_id = $1
		ORDER BY ak.created_at DESC
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type keyItem struct {
		ID                  string   `json:"id"`
		OrgID               *string  `json:"org_id,omitempty"`
		OwnerType           string   `json:"owner_type"`
		UserID              *string  `json:"user_id,omitempty"`
		UserName            *string  `json:"user_name,omitempty"`
		AgentID             *string  `json:"agent_id,omitempty"`
		AgentName           *string  `json:"agent_name,omitempty"`
		AgentSlug           *string  `json:"agent_slug,omitempty"`
		Name                string   `json:"name"`
		ScopeAll            bool     `json:"scope_all_projects"`
		ProjectIDs          []string `json:"allowed_project_ids"`
		AllowedProjectNames []string `json:"allowed_project_names"`
		CreatedAt           string   `json:"created_at"`
		LastUsedAt          *string  `json:"last_used_at"`
	}
	var keys []keyItem
	for rows.Next() {
		var key models.APIKey
		var userName *string
		var agentName *string
		var agentSlug *string
		var allowedProjectNames []string
		if err := rows.Scan(
			&key.ID,
			&key.OrgID,
			&key.OwnerType,
			&key.UserID,
			&userName,
			&key.AgentID,
			&agentName,
			&agentSlug,
			&key.Name,
			&key.ScopeAllProjects,
			&key.AllowedProjectIDs,
			&allowedProjectNames,
			&key.CreatedAt,
			&key.LastUsedAt,
		); err != nil {
			continue
		}
		k := keyItem{
			ID:                  key.ID,
			OrgID:               key.OrgID,
			OwnerType:           key.OwnerType,
			UserID:              key.UserID,
			UserName:            userName,
			AgentID:             key.AgentID,
			AgentName:           agentName,
			AgentSlug:           agentSlug,
			Name:                key.Name,
			ScopeAll:            key.ScopeAllProjects,
			ProjectIDs:          key.AllowedProjectIDs,
			AllowedProjectNames: allowedProjectNames,
			CreatedAt:           key.CreatedAt.Format(time.RFC3339),
		}
		if key.LastUsedAt != nil {
			formatted := key.LastUsedAt.Format(time.RFC3339)
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
