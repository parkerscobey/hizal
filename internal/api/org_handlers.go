package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrgHandlers struct {
	pool *pgxpool.Pool
}

func NewOrgHandlers(pool *pgxpool.Pool) *OrgHandlers {
	return &OrgHandlers{pool: pool}
}

// requireOrgRole checks the current JWT user's role in an org and returns error if insufficient.
// Hierarchy: owner > admin > member > viewer
func requireOrgRole(r *http.Request, pool *pgxpool.Pool, orgID string, roles ...string) (string, error) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		return "", errors.New("not authenticated")
	}
	var role string
	err := pool.QueryRow(r.Context(), `
		SELECT role FROM org_memberships WHERE user_id = $1 AND org_id = $2
	`, user.ID, orgID).Scan(&role)
	if err != nil {
		return "", errors.New("not a member of this org")
	}
	for _, allowed := range roles {
		if role == allowed {
			return role, nil
		}
	}
	return role, errors.New("insufficient permissions")
}

// POST /v1/orgs
func (h *OrgHandlers) CreateOrg(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer tx.Rollback(r.Context())

	var orgID string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO orgs (name, slug) VALUES ($1, $2) RETURNING id
	`, body.Name, body.Slug).Scan(&orgID)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "an org with that slug already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	_, err = tx.Exec(r.Context(), `
		INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')
	`, user.ID, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":   orgID,
		"name": body.Name,
		"slug": body.Slug,
		"role": "owner",
	})
}

// GET /v1/orgs
func (h *OrgHandlers) ListOrgs(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT o.id, o.name, o.slug, o.tier, o.created_at, om.role
		FROM orgs o
		JOIN org_memberships om ON om.org_id = o.id
		WHERE om.user_id = $1
		ORDER BY o.created_at
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type orgItem struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Slug      string `json:"slug"`
		Tier      string `json:"tier"`
		CreatedAt string `json:"created_at"`
		Role      string `json:"role"`
	}
	var orgs []orgItem
	for rows.Next() {
		var o orgItem
		var createdAt time.Time
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Tier, &createdAt, &o.Role); err != nil {
			continue
		}
		o.CreatedAt = createdAt.Format(time.RFC3339)
		orgs = append(orgs, o)
	}
	if orgs == nil {
		orgs = []orgItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"orgs": orgs})
}

// GET /v1/orgs/:id
func (h *OrgHandlers) GetOrg(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	// Verify membership
	var callerRole string
	err := h.pool.QueryRow(r.Context(), `
		SELECT om.role FROM org_memberships om WHERE om.user_id = $1 AND om.org_id = $2
	`, user.ID, orgID).Scan(&callerRole)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this org")
		return
	}

	var orgName, orgSlug, orgTier string
	err = h.pool.QueryRow(r.Context(), `SELECT name, slug, tier FROM orgs WHERE id = $1`, orgID).Scan(&orgName, &orgSlug, &orgTier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "org not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Fetch members
	rows, err := h.pool.Query(r.Context(), `
		SELECT u.id, u.email, u.name, om.role, om.created_at
		FROM users u
		JOIN org_memberships om ON om.user_id = u.id
		WHERE om.org_id = $1
		ORDER BY om.created_at
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type member struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		Role      string `json:"role"`
		JoinedAt  string `json:"joined_at"`
	}
	var members []member
	for rows.Next() {
		var m member
		var joinedAt time.Time
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.Role, &joinedAt); err != nil {
			continue
		}
		m.JoinedAt = joinedAt.Format(time.RFC3339)
		members = append(members, m)
	}
	if members == nil {
		members = []member{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      orgID,
		"name":    orgName,
		"slug":    orgSlug,
		"tier":    orgTier,
		"role":    callerRole,
		"members": members,
	})
}

// PATCH /v1/orgs/:id
func (h *OrgHandlers) UpdateOrg(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
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

	_, err := h.pool.Exec(r.Context(), `UPDATE orgs SET name = $1, updated_at = NOW() WHERE id = $2`, body.Name, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": orgID, "name": body.Name})
}

// POST /v1/orgs/:id/members — invite user by email
func (h *OrgHandlers) InviteMember(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
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
	if !isValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "role must be owner, admin, member, or viewer")
		return
	}

	// Find user by email
	var userID string
	err := h.pool.QueryRow(r.Context(), `SELECT id FROM users WHERE email = $1`, body.Email).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "no user with that email")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, org_id) DO UPDATE SET role = EXCLUDED.role
	`, userID, orgID, body.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user_id": userID,
		"org_id":  orgID,
		"email":   body.Email,
		"role":    body.Role,
	})
}

// DELETE /v1/orgs/:id/members/:userId
func (h *OrgHandlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	targetUserID := chi.URLParam(r, "userId")

	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	_, err := h.pool.Exec(r.Context(), `
		DELETE FROM org_memberships WHERE user_id = $1 AND org_id = $2
	`, targetUserID, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /v1/orgs/:id/members/:userId
func (h *OrgHandlers) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	targetUserID := chi.URLParam(r, "userId")

	if _, err := requireOrgRole(r, h.pool, orgID, "owner"); err != nil {
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
	if !isValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "role must be owner, admin, member, or viewer")
		return
	}

	_, err := h.pool.Exec(r.Context(), `
		UPDATE org_memberships SET role = $1 WHERE user_id = $2 AND org_id = $3
	`, body.Role, targetUserID, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id": targetUserID,
		"org_id":  orgID,
		"role":    body.Role,
	})
}

func isValidRole(role string) bool {
	switch role {
	case "owner", "admin", "member", "viewer":
		return true
	}
	return false
}
