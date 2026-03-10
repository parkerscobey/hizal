package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requireProjectAccess checks whether the caller has access to a project.
//
// Access rules:
//   - Org owners are implicitly members of all projects (no explicit record needed).
//   - All other users must have an explicit project_memberships row.
//
// Returns the caller's effective role and the project's org_id, or an error.
func requireProjectAccess(r *http.Request, pool *pgxpool.Pool, projectID string, roles ...string) (role string, orgID string, err error) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		return "", "", errors.New("not authenticated")
	}

	// Resolve org for this project.
	if err := pool.QueryRow(r.Context(),
		`SELECT org_id FROM projects WHERE id = $1`, projectID,
	).Scan(&orgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errors.New("project not found")
		}
		return "", "", err
	}

	// Org owners bypass explicit project membership.
	var orgRole string
	_ = pool.QueryRow(r.Context(),
		`SELECT role FROM org_memberships WHERE user_id = $1 AND org_id = $2`,
		user.ID, orgID,
	).Scan(&orgRole)

	if orgRole == "owner" {
		if len(roles) == 0 {
			return "owner", orgID, nil
		}
		for _, r := range roles {
			if r == "owner" || r == "admin" || r == "member" || r == "viewer" {
				return "owner", orgID, nil
			}
		}
		return "owner", orgID, nil
	}

	// Check explicit project membership.
	var projectRole string
	err = pool.QueryRow(r.Context(),
		`SELECT role FROM project_memberships WHERE user_id = $1 AND project_id = $2`,
		user.ID, projectID,
	).Scan(&projectRole)
	if err != nil {
		return "", "", errors.New("not a member of this project")
	}

	if len(roles) > 0 {
		allowed := false
		for _, r := range roles {
			if r == projectRole {
				allowed = true
				break
			}
		}
		if !allowed {
			return projectRole, orgID, errors.New("insufficient permissions")
		}
	}

	return projectRole, orgID, nil
}

// isValidProjectRole validates the project-level role string.
func isValidProjectRole(role string) bool {
	switch role {
	case "admin", "member", "viewer":
		return true
	}
	return false
}

// ProjectMembershipHandlers handles project membership endpoints.
type ProjectMembershipHandlers struct {
	pool *pgxpool.Pool
}

func NewProjectMembershipHandlers(pool *pgxpool.Pool) *ProjectMembershipHandlers {
	return &ProjectMembershipHandlers{pool: pool}
}

// POST /v1/projects/:id/members
func (h *ProjectMembershipHandlers) AddMember(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")

	// Only org owners/admins can manage project membership.
	_, orgID, err := requireProjectAccess(r, h.pool, projectID, "owner", "admin")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "email is required")
		return
	}
	if body.Role == "" {
		body.Role = "member"
	}
	if !isValidProjectRole(body.Role) {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "role must be admin, member, or viewer")
		return
	}

	// Target user must exist.
	var userID string
	err = h.pool.QueryRow(r.Context(), `SELECT id FROM users WHERE email = $1`, body.Email).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "no user with that email")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Target user must be a member of the org first.
	var orgMemberRole string
	err = h.pool.QueryRow(r.Context(),
		`SELECT role FROM org_memberships WHERE user_id = $1 AND org_id = $2`, userID, orgID,
	).Scan(&orgMemberRole)
	if err != nil {
		writeError(w, http.StatusBadRequest, "NOT_ORG_MEMBER", "user must be an org member before being added to a project")
		return
	}

	membershipID := uuid.New().String()
	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO project_memberships (id, user_id, project_id, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, project_id) DO UPDATE SET role = EXCLUDED.role
	`, membershipID, userID, projectID, body.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user_id":    userID,
		"project_id": projectID,
		"email":      body.Email,
		"role":       body.Role,
	})
}

// GET /v1/projects/:id/members
func (h *ProjectMembershipHandlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")

	if _, _, err := requireProjectAccess(r, h.pool, projectID); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT u.id, u.email, u.name, pm.role, pm.created_at
		FROM users u
		JOIN project_memberships pm ON pm.user_id = u.id
		WHERE pm.project_id = $1
		ORDER BY pm.created_at
	`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type memberItem struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		JoinedAt string `json:"joined_at"`
	}
	var members []memberItem
	for rows.Next() {
		var m memberItem
		var joinedAt time.Time
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.Role, &joinedAt); err != nil {
			continue
		}
		m.JoinedAt = joinedAt.Format(time.RFC3339)
		members = append(members, m)
	}
	if members == nil {
		members = []memberItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"members": members})
}

// PATCH /v1/projects/:id/members/:userId
func (h *ProjectMembershipHandlers) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	targetUserID := chi.URLParam(r, "userId")

	if _, _, err := requireProjectAccess(r, h.pool, projectID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Role == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "role is required")
		return
	}
	if !isValidProjectRole(body.Role) {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "role must be admin, member, or viewer")
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`UPDATE project_memberships SET role = $1 WHERE user_id = $2 AND project_id = $3`,
		body.Role, targetUserID, projectID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "membership not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":    targetUserID,
		"project_id": projectID,
		"role":       body.Role,
	})
}

// DELETE /v1/projects/:id/members/:userId
func (h *ProjectMembershipHandlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	targetUserID := chi.URLParam(r, "userId")

	if _, _, err := requireProjectAccess(r, h.pool, projectID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`DELETE FROM project_memberships WHERE user_id = $1 AND project_id = $2`,
		targetUserID, projectID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
