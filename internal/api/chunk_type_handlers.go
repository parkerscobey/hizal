package api

import (
	"encoding/json"
	"net/http"

	"github.com/XferOps/winnow/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChunkTypeHandlers struct {
	pool *pgxpool.Pool
}

func NewChunkTypeHandlers(pool *pgxpool.Pool) *ChunkTypeHandlers {
	return &ChunkTypeHandlers{pool: pool}
}

func (h *ChunkTypeHandlers) ListChunkTypes(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin", "member", "viewer"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, org_id, name, slug, description, default_scope, default_always_inject, consolidation_behavior, created_at, updated_at
		FROM chunk_types
		WHERE org_id IS NULL OR org_id = $1
		ORDER BY org_id NULLS FIRST, name
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var types []models.ChunkType
	for rows.Next() {
		var t models.ChunkType
		if err := rows.Scan(
			&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.Description,
			&t.DefaultScope, &t.DefaultAlwaysInject, &t.ConsolidationBehavior,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			continue
		}
		types = append(types, t)
	}
	if types == nil {
		types = []models.ChunkType{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"chunk_types": types})
}

func (h *ChunkTypeHandlers) CreateChunkType(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name                  string `json:"name"`
		Slug                  string `json:"slug"`
		Description           string `json:"description"`
		DefaultScope          string `json:"default_scope"`
		DefaultAlwaysInject   bool   `json:"default_always_inject"`
		ConsolidationBehavior string `json:"consolidation_behavior"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}

	if body.DefaultScope == "" {
		body.DefaultScope = "PROJECT"
	}
	if body.ConsolidationBehavior == "" {
		body.ConsolidationBehavior = "SURFACE"
	}

	var t models.ChunkType
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO chunk_types (org_id, name, slug, description, default_scope, default_always_inject, consolidation_behavior)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id, name, slug, description, default_scope, default_always_inject, consolidation_behavior, created_at, updated_at
	`, orgID, body.Name, body.Slug, nullableStr(body.Description), body.DefaultScope, body.DefaultAlwaysInject, body.ConsolidationBehavior).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.Description,
		&t.DefaultScope, &t.DefaultAlwaysInject, &t.ConsolidationBehavior,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "a chunk type with that slug already exists in this org")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, t)
}

func (h *ChunkTypeHandlers) GetChunkType(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "id")

	var t models.ChunkType
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, name, slug, description, default_scope, default_always_inject, consolidation_behavior, created_at, updated_at
		FROM chunk_types WHERE id = $1
	`, typeID).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.Description,
		&t.DefaultScope, &t.DefaultAlwaysInject, &t.ConsolidationBehavior,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chunk type not found")
		return
	}

	if t.OrgID != nil {
		if _, err := requireOrgRole(r, h.pool, *t.OrgID, "owner", "admin", "member", "viewer"); err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *ChunkTypeHandlers) UpdateChunkType(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "id")

	var orgID *string
	err := h.pool.QueryRow(r.Context(), `SELECT org_id FROM chunk_types WHERE id = $1`, typeID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chunk type not found")
		return
	}

	if orgID == nil {
		writeError(w, http.StatusForbidden, "GLOBAL_TYPE", "global presets cannot be modified")
		return
	}

	if _, err := requireOrgRole(r, h.pool, *orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name                  *string `json:"name"`
		Description           *string `json:"description"`
		DefaultScope          *string `json:"default_scope"`
		DefaultAlwaysInject   *bool   `json:"default_always_inject"`
		ConsolidationBehavior *string `json:"consolidation_behavior"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		UPDATE chunk_types SET
		  name                  = COALESCE($2, name),
		  description           = COALESCE($3, description),
		  default_scope         = COALESCE($4, default_scope),
		  default_always_inject = COALESCE($5, default_always_inject),
		  consolidation_behavior= COALESCE($6, consolidation_behavior),
		  updated_at            = NOW()
		WHERE id = $1
	`, typeID, body.Name, body.Description, body.DefaultScope, body.DefaultAlwaysInject, body.ConsolidationBehavior)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	var t models.ChunkType
	err = h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, name, slug, description, default_scope, default_always_inject, consolidation_behavior, created_at, updated_at
		FROM chunk_types WHERE id = $1
	`, typeID).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.Description,
		&t.DefaultScope, &t.DefaultAlwaysInject, &t.ConsolidationBehavior,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *ChunkTypeHandlers) DeleteChunkType(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "id")

	var orgID *string
	err := h.pool.QueryRow(r.Context(), `SELECT org_id FROM chunk_types WHERE id = $1`, typeID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chunk type not found")
		return
	}

	if orgID == nil {
		writeError(w, http.StatusForbidden, "GLOBAL_TYPE", "global presets cannot be deleted")
		return
	}

	if _, err := requireOrgRole(r, h.pool, *orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	_, err = h.pool.Exec(r.Context(), `DELETE FROM chunk_types WHERE id = $1`, typeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
