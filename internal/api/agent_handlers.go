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

var validAgentTypes = map[string]bool{
	"ASSISTANT": true, "CODER": true, "QA": true, "OPS": true, "CUSTOM": true,
}

var validAgentStatuses = map[string]bool{
	"ACTIVE": true, "INACTIVE": true, "SUSPENDED": true,
}

type AgentHandlers struct {
	pool *pgxpool.Pool
}

func NewAgentHandlers(pool *pgxpool.Pool) *AgentHandlers {
	return &AgentHandlers{pool: pool}
}

// requireAgentAccess verifies the caller owns or is an org admin/owner of the agent.
// Returns the agent row's org_id and owner_id.
func requireAgentAccess(r *http.Request, pool *pgxpool.Pool, agentID string, roles ...string) (orgID, ownerID string, err error) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		return "", "", errors.New("not authenticated")
	}

	err = pool.QueryRow(r.Context(),
		`SELECT org_id, owner_id FROM agents WHERE id = $1`, agentID,
	).Scan(&orgID, &ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errors.New("agent not found")
		}
		return "", "", err
	}

	// Owner always has access.
	if user.ID == ownerID {
		return orgID, ownerID, nil
	}

	// Org owner/admin also has access if roles permit.
	var orgRole string
	_ = pool.QueryRow(r.Context(),
		`SELECT role FROM org_memberships WHERE user_id = $1 AND org_id = $2`, user.ID, orgID,
	).Scan(&orgRole)

	if len(roles) == 0 {
		if orgRole == "owner" || orgRole == "admin" {
			return orgID, ownerID, nil
		}
	} else {
		for _, r := range roles {
			if r == orgRole {
				return orgID, ownerID, nil
			}
		}
	}

	return "", "", errors.New("forbidden")
}

// POST /v1/orgs/:id/agents
func (h *AgentHandlers) CreateAgent(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	// Caller must be an org member.
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin", "member"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Platform    string `json:"platform"`
		InstanceID  string `json:"instance_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}
	if body.Type == "" {
		body.Type = "CUSTOM"
	}
	if !validAgentTypes[body.Type] {
		writeError(w, http.StatusBadRequest, "INVALID_TYPE", "type must be ASSISTANT, CODER, QA, OPS, or CUSTOM")
		return
	}

	agentID := uuid.New().String()
	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, description, platform, instance_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, agentID, orgID, user.ID, body.Name, body.Slug, body.Type,
		nullableStr(body.Description), nullableStr(body.Platform), nullableStr(body.InstanceID))
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "an agent with that slug already exists in this org")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       agentID,
		"org_id":   orgID,
		"owner_id": user.ID,
		"name":     body.Name,
		"slug":     body.Slug,
		"type":     body.Type,
		"status":   "ACTIVE",
	})
}

// GET /v1/orgs/:id/agents
func (h *AgentHandlers) ListAgents(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin", "member", "viewer"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, owner_id, name, slug, type, description, status, platform,
		       instance_id, ip_address, last_active_at, created_at
		FROM agents WHERE org_id = $1 ORDER BY created_at
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type agentItem struct {
		ID           string  `json:"id"`
		OwnerID      string  `json:"owner_id"`
		Name         string  `json:"name"`
		Slug         string  `json:"slug"`
		Type         string  `json:"type"`
		Description  *string `json:"description"`
		Status       string  `json:"status"`
		Platform     *string `json:"platform"`
		InstanceID   *string `json:"instance_id"`
		IPAddress    *string `json:"ip_address"`
		LastActiveAt *string `json:"last_active_at"`
		CreatedAt    string  `json:"created_at"`
	}
	var agents []agentItem
	for rows.Next() {
		var a agentItem
		var createdAt time.Time
		var lastActiveAt *time.Time
		if err := rows.Scan(
			&a.ID, &a.OwnerID, &a.Name, &a.Slug, &a.Type, &a.Description,
			&a.Status, &a.Platform, &a.InstanceID, &a.IPAddress, &lastActiveAt, &createdAt,
		); err != nil {
			continue
		}
		a.CreatedAt = createdAt.Format(time.RFC3339)
		if lastActiveAt != nil {
			s := lastActiveAt.Format(time.RFC3339)
			a.LastActiveAt = &s
		}
		agents = append(agents, a)
	}
	if agents == nil {
		agents = []agentItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"agents": agents})
}

// GET /v1/agents/:id
func (h *AgentHandlers) GetAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	orgID, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin", "member", "viewer")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var (
		ownerID, name, slug, agentType, status string
		description, platform, instanceID, ipAddress *string
		lastActiveAt                                  *time.Time
		createdAt, updatedAt                          time.Time
	)
	err = h.pool.QueryRow(r.Context(), `
		SELECT owner_id, name, slug, type, description, status, platform,
		       instance_id, ip_address, last_active_at, created_at, updated_at
		FROM agents WHERE id = $1
	`, agentID).Scan(
		&ownerID, &name, &slug, &agentType, &description, &status,
		&platform, &instanceID, &ipAddress, &lastActiveAt, &createdAt, &updatedAt,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "agent not found")
		return
	}

	// Fetch assigned projects.
	rows, _ := h.pool.Query(r.Context(), `
		SELECT p.id, p.name, p.slug FROM projects p
		JOIN agent_projects ap ON ap.project_id = p.id
		WHERE ap.agent_id = $1 ORDER BY p.created_at
	`, agentID)
	defer rows.Close()

	type projectRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	var projects []projectRef
	for rows.Next() {
		var p projectRef
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug); err == nil {
			projects = append(projects, p)
		}
	}
	if projects == nil {
		projects = []projectRef{}
	}

	resp := map[string]interface{}{
		"id":         agentID,
		"org_id":     orgID,
		"owner_id":   ownerID,
		"name":       name,
		"slug":       slug,
		"type":       agentType,
		"description": description,
		"status":     status,
		"platform":   platform,
		"instance_id": instanceID,
		"ip_address": ipAddress,
		"projects":   projects,
		"created_at": createdAt.Format(time.RFC3339),
		"updated_at": updatedAt.Format(time.RFC3339),
	}
	if lastActiveAt != nil {
		s := lastActiveAt.Format(time.RFC3339)
		resp["last_active_at"] = s
	}
	writeJSON(w, http.StatusOK, resp)
}

// PATCH /v1/agents/:id
func (h *AgentHandlers) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Status      *string `json:"status"`
		Platform    *string `json:"platform"`
		InstanceID  *string `json:"instance_id"`
		IPAddress   *string `json:"ip_address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if body.Status != nil && !validAgentStatuses[*body.Status] {
		writeError(w, http.StatusBadRequest, "INVALID_STATUS", "status must be ACTIVE, INACTIVE, or SUSPENDED")
		return
	}

	_, err := h.pool.Exec(r.Context(), `
		UPDATE agents SET
		  name        = COALESCE($2, name),
		  description = COALESCE($3, description),
		  status      = COALESCE($4, status),
		  platform    = COALESCE($5, platform),
		  instance_id = COALESCE($6, instance_id),
		  ip_address  = COALESCE($7, ip_address),
		  updated_at  = NOW()
		WHERE id = $1
	`, agentID, body.Name, body.Description, body.Status,
		body.Platform, body.InstanceID, body.IPAddress)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": agentID, "updated": true})
}

// DELETE /v1/agents/:id — deletes agent and cascades to keys + project assignments
func (h *AgentHandlers) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	_, err := h.pool.Exec(r.Context(), `DELETE FROM agents WHERE id = $1`, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/agents/:id/projects — assign agent to a project
func (h *AgentHandlers) AddProject(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	_, ownerID, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "project_id is required")
		return
	}

	// Verify owner has access to this project (org owner bypass is handled inside).
	var orgID string
	err = h.pool.QueryRow(r.Context(), `SELECT org_id FROM projects WHERE id = $1`, body.ProjectID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}

	var ownerOrgRole string
	_ = h.pool.QueryRow(r.Context(),
		`SELECT role FROM org_memberships WHERE user_id = $1 AND org_id = $2`, ownerID, orgID,
	).Scan(&ownerOrgRole)

	if ownerOrgRole != "owner" {
		// Non-owner must have an explicit project membership.
		var pmRole string
		err = h.pool.QueryRow(r.Context(),
			`SELECT role FROM project_memberships WHERE user_id = $1 AND project_id = $2`,
			ownerID, body.ProjectID,
		).Scan(&pmRole)
		if err != nil {
			writeError(w, http.StatusForbidden, "OWNER_NOT_PROJECT_MEMBER",
				"agent's owner does not have access to this project")
			return
		}
	}

	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO agent_projects (agent_id, project_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		agentID, body.ProjectID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"agent_id":   agentID,
		"project_id": body.ProjectID,
	})
}

// DELETE /v1/agents/:id/projects/:projectId
func (h *AgentHandlers) RemoveProject(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	projectID := chi.URLParam(r, "projectId")

	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`DELETE FROM agent_projects WHERE agent_id = $1 AND project_id = $2`, agentID, projectID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nullableStr returns nil for empty strings, useful for optional DB fields.
func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
