package api

import (
	"encoding/json"
	"net/http"

	"github.com/XferOps/winnow/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentTypeHandlers struct {
	pool *pgxpool.Pool
}

func NewAgentTypeHandlers(pool *pgxpool.Pool) *AgentTypeHandlers {
	return &AgentTypeHandlers{pool: pool}
}

func (h *AgentTypeHandlers) ListAgentTypes(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin", "member", "viewer"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, org_id, name, slug, base_type, description, inject_filters, search_filters, created_at, updated_at
		FROM agent_types
		WHERE org_id IS NULL OR org_id = $1
		ORDER BY org_id NULLS FIRST, name
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var types []models.AgentType
	for rows.Next() {
		var t models.AgentType
		var injectFiltersJSON, searchFiltersJSON []byte
		if err := rows.Scan(
			&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.BaseType, &t.Description,
			&injectFiltersJSON, &searchFiltersJSON, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			continue
		}
		json.Unmarshal(injectFiltersJSON, &t.InjectFilters)
		json.Unmarshal(searchFiltersJSON, &t.SearchFilters)
		types = append(types, t)
	}
	if types == nil {
		types = []models.AgentType{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"agent_types": types})
}

func (h *AgentTypeHandlers) CreateAgentType(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name          string                       `json:"name"`
		Slug          string                       `json:"slug"`
		BaseType      string                       `json:"base_type"`
		Description   string                       `json:"description"`
		InjectFilters models.AgentTypeFilterConfig `json:"inject_filters"`
		SearchFilters models.AgentTypeFilterConfig `json:"search_filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}

	injectFiltersJSON, _ := json.Marshal(body.InjectFilters)
	searchFiltersJSON, _ := json.Marshal(body.SearchFilters)

	var t models.AgentType
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO agent_types (org_id, name, slug, base_type, description, inject_filters, search_filters)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id, name, slug, base_type, description, inject_filters, search_filters, created_at, updated_at
	`, orgID, body.Name, body.Slug, nullableStr(body.BaseType), nullableStr(body.Description),
		injectFiltersJSON, searchFiltersJSON).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.BaseType, &t.Description,
		&injectFiltersJSON, &searchFiltersJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "an agent type with that slug already exists in this org")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	json.Unmarshal(injectFiltersJSON, &t.InjectFilters)
	json.Unmarshal(searchFiltersJSON, &t.SearchFilters)

	writeJSON(w, http.StatusCreated, t)
}

func (h *AgentTypeHandlers) GetAgentType(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "id")

	var t models.AgentType
	var injectFiltersJSON, searchFiltersJSON []byte
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, name, slug, base_type, description, inject_filters, search_filters, created_at, updated_at
		FROM agent_types WHERE id = $1
	`, typeID).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.BaseType, &t.Description,
		&injectFiltersJSON, &searchFiltersJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "agent type not found")
		return
	}
	json.Unmarshal(injectFiltersJSON, &t.InjectFilters)
	json.Unmarshal(searchFiltersJSON, &t.SearchFilters)

	if t.OrgID != nil {
		if _, err := requireOrgRole(r, h.pool, *t.OrgID, "owner", "admin", "member", "viewer"); err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *AgentTypeHandlers) UpdateAgentType(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "id")

	var orgID *string
	err := h.pool.QueryRow(r.Context(), `SELECT org_id FROM agent_types WHERE id = $1`, typeID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "agent type not found")
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
		Name          *string                       `json:"name"`
		Description   *string                       `json:"description"`
		InjectFilters *models.AgentTypeFilterConfig `json:"inject_filters"`
		SearchFilters *models.AgentTypeFilterConfig `json:"search_filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	var injectFiltersJSON, searchFiltersJSON []byte
	if body.InjectFilters != nil {
		injectFiltersJSON, _ = json.Marshal(body.InjectFilters)
	}
	if body.SearchFilters != nil {
		searchFiltersJSON, _ = json.Marshal(body.SearchFilters)
	}

	_, err = h.pool.Exec(r.Context(), `
		UPDATE agent_types SET
		  name           = COALESCE($2, name),
		  description    = COALESCE($3, description),
		  inject_filters = COALESCE($4, inject_filters),
		  search_filters = COALESCE($5, search_filters),
		  updated_at     = NOW()
		WHERE id = $1
	`, typeID, body.Name, body.Description,
		nullableBytes(injectFiltersJSON), nullableBytes(searchFiltersJSON))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	var t models.AgentType
	err = h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, name, slug, base_type, description, inject_filters, search_filters, created_at, updated_at
		FROM agent_types WHERE id = $1
	`, typeID).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.BaseType, &t.Description,
		&injectFiltersJSON, &searchFiltersJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	json.Unmarshal(injectFiltersJSON, &t.InjectFilters)
	json.Unmarshal(searchFiltersJSON, &t.SearchFilters)

	writeJSON(w, http.StatusOK, t)
}

func (h *AgentTypeHandlers) DeleteAgentType(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "id")

	var orgID *string
	err := h.pool.QueryRow(r.Context(), `SELECT org_id FROM agent_types WHERE id = $1`, typeID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "agent type not found")
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

	_, err = h.pool.Exec(r.Context(), `DELETE FROM agent_types WHERE id = $1`, typeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nullableBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
