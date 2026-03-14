package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/XferOps/winnow/internal/billing"
	"github.com/XferOps/winnow/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProjectHandlers struct {
	pool *pgxpool.Pool
}

func NewProjectHandlers(pool *pgxpool.Pool) *ProjectHandlers {
	return &ProjectHandlers{pool: pool}
}

// POST /v1/orgs/:id/projects
func (h *ProjectHandlers) CreateProject(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name        string  `json:"name"`
		Slug        string  `json:"slug"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}

	// Enforce tier project limit
	var tier string
	h.pool.QueryRow(r.Context(), `SELECT tier FROM orgs WHERE id = $1`, orgID).Scan(&tier)
	limits := billing.For(tier)
	if limits.ProjectLimit >= 0 {
		var projectCount int
		h.pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM projects WHERE org_id = $1`, orgID).Scan(&projectCount)
		if projectCount >= limits.ProjectLimit {
			writeError(w, http.StatusPaymentRequired, "PROJECT_LIMIT_REACHED",
				"You've reached the project limit for your plan. Upgrade to create more projects.")
			return
		}
	}

	var project models.Project
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO projects (org_id, name, slug, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, org_id, name, slug, description
	`, orgID, body.Name, body.Slug, body.Description).Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.Description)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "a project with that slug already exists in this org")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":          project.ID,
		"org_id":      project.OrgID,
		"name":        project.Name,
		"slug":        project.Slug,
		"description": project.Description,
	})
}

// GET /v1/orgs/:id/projects
func (h *ProjectHandlers) ListProjects(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	orgRole, err := requireOrgRole(r, h.pool, orgID, "owner", "admin", "member", "viewer")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT p.id, p.org_id, p.name, p.slug, p.description, p.created_at, p.locked_at, pm.role
		FROM projects p
		LEFT JOIN project_memberships pm ON pm.project_id = p.id AND pm.user_id = $2
		WHERE p.org_id = $1
		ORDER BY p.created_at
	`, orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type projectItem struct {
		ID            string  `json:"id"`
		OrgID         string  `json:"org_id"`
		Name          string  `json:"name"`
		Slug          string  `json:"slug"`
		Description   *string `json:"description,omitempty"`
		CreatedAt     string  `json:"created_at"`
		Locked        bool    `json:"locked"`
		IsMember      bool    `json:"is_member"`
		EffectiveRole *string `json:"effective_role"`
		CanOpen       bool    `json:"can_open"`
	}
	var projects []projectItem
	for rows.Next() {
		var project models.Project
		var lockedAt *time.Time
		var projectRole *string
		if err := rows.Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.Description, &project.CreatedAt, &lockedAt, &projectRole); err != nil {
			continue
		}

		isMember := false
		effectiveRole := projectRole
		canOpen := false
		if orgRole == "owner" {
			isMember = true
			ownerRole := "owner"
			effectiveRole = &ownerRole
			canOpen = true
		} else if projectRole != nil {
			isMember = true
			canOpen = true
		}

		projects = append(projects, projectItem{
			ID:            project.ID,
			OrgID:         project.OrgID,
			Name:          project.Name,
			Slug:          project.Slug,
			Description:   project.Description,
			CreatedAt:     project.CreatedAt.Format(time.RFC3339),
			Locked:        lockedAt != nil,
			IsMember:      isMember,
			EffectiveRole: effectiveRole,
			CanOpen:       canOpen,
		})
	}
	if projects == nil {
		projects = []projectItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": projects})
}

// PATCH /v1/projects/:id
func (h *ProjectHandlers) UpdateProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")

	// Look up orgID for this project
	var project models.Project
	err := h.pool.QueryRow(r.Context(), `SELECT id, org_id FROM projects WHERE id = $1`, projectID).Scan(&project.ID, &project.OrgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if _, err := requireOrgRole(r, h.pool, project.OrgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if body.Name == nil && body.Slug == nil && body.Description == nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name, slug, or description is required")
		return
	}
	if body.Name != nil && *body.Name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name cannot be empty")
		return
	}
	if body.Slug != nil && *body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "slug cannot be empty")
		return
	}

	err = h.pool.QueryRow(r.Context(), `
		UPDATE projects
		SET
			name = COALESCE($1, name),
			slug = COALESCE($2, slug),
			description = CASE
				WHEN $3::text IS NULL THEN description
				ELSE $3
			END,
			updated_at = NOW()
		WHERE id = $4
		RETURNING id, org_id, name, slug, description
	`, body.Name, body.Slug, body.Description, project.ID).Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.Description)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "a project with that slug already exists in this org")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":          project.ID,
		"org_id":      project.OrgID,
		"name":        project.Name,
		"slug":        project.Slug,
		"description": project.Description,
	})
}
