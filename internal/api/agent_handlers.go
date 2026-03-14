package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/XferOps/winnow/internal/billing"
	"github.com/XferOps/winnow/internal/models"
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

	// Enforce tier: agents are not available on Free/Pro
	var tier string
	h.pool.QueryRow(r.Context(), `SELECT tier FROM orgs WHERE id = $1`, orgID).Scan(&tier)
	if !billing.For(tier).AllowAgents {
		writeError(w, http.StatusForbidden, "AGENTS_NOT_AVAILABLE",
			"Agent management requires a Team plan. Use a user-scoped API key instead.")
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

	agent := models.Agent{
		ID:      uuid.New().String(),
		OrgID:   orgID,
		OwnerID: user.ID,
		Name:    body.Name,
		Slug:    body.Slug,
		Type:    body.Type,
		Status:  "ACTIVE",
	}
	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, description, platform, instance_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, agent.ID, agent.OrgID, agent.OwnerID, agent.Name, agent.Slug, agent.Type,
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
		"id":       agent.ID,
		"org_id":   agent.OrgID,
		"owner_id": agent.OwnerID,
		"name":     agent.Name,
		"slug":     agent.Slug,
		"type":     agent.Type,
		"status":   agent.Status,
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
		var agent models.Agent
		if err := rows.Scan(
			&agent.ID, &agent.OwnerID, &agent.Name, &agent.Slug, &agent.Type, &agent.Description,
			&agent.Status, &agent.Platform, &agent.InstanceID, &agent.IPAddress, &agent.LastActiveAt, &agent.CreatedAt,
		); err != nil {
			continue
		}
		item := agentItem{
			ID:          agent.ID,
			OwnerID:     agent.OwnerID,
			Name:        agent.Name,
			Slug:        agent.Slug,
			Type:        agent.Type,
			Description: agent.Description,
			Status:      agent.Status,
			Platform:    agent.Platform,
			InstanceID:  agent.InstanceID,
			IPAddress:   agent.IPAddress,
			CreatedAt:   agent.CreatedAt.Format(time.RFC3339),
		}
		if agent.LastActiveAt != nil {
			s := agent.LastActiveAt.Format(time.RFC3339)
			item.LastActiveAt = &s
		}
		agents = append(agents, item)
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

	var agent models.Agent
	err = h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, owner_id, name, slug, type, description, status, platform,
		       instance_id, ip_address, last_active_at, created_at, updated_at
		FROM agents WHERE id = $1
	`, agentID).Scan(
		&agent.ID, &agent.OrgID, &agent.OwnerID, &agent.Name, &agent.Slug, &agent.Type, &agent.Description,
		&agent.Status, &agent.Platform, &agent.InstanceID, &agent.IPAddress, &agent.LastActiveAt, &agent.CreatedAt, &agent.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "agent not found")
		return
	}

	// Fetch assigned projects.
	rows, _ := h.pool.Query(r.Context(), `
		SELECT p.id, p.name, p.slug, p.description FROM projects p
		JOIN agent_projects ap ON ap.project_id = p.id
		WHERE ap.agent_id = $1 ORDER BY p.created_at
	`, agentID)
	defer rows.Close()

	type projectRef struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Slug        string  `json:"slug"`
		Description *string `json:"description,omitempty"`
	}
	var projects []projectRef
	for rows.Next() {
		var project models.Project
		if err := rows.Scan(&project.ID, &project.Name, &project.Slug, &project.Description); err == nil {
			projects = append(projects, projectRef{
				ID:          project.ID,
				Name:        project.Name,
				Slug:        project.Slug,
				Description: project.Description,
			})
		}
	}
	if projects == nil {
		projects = []projectRef{}
	}

	resp := map[string]interface{}{
		"id":          agent.ID,
		"org_id":      orgID,
		"owner_id":    agent.OwnerID,
		"name":        agent.Name,
		"slug":        agent.Slug,
		"type":        agent.Type,
		"description": agent.Description,
		"status":      agent.Status,
		"platform":    agent.Platform,
		"instance_id": agent.InstanceID,
		"ip_address":  agent.IPAddress,
		"projects":    projects,
		"created_at":  agent.CreatedAt.Format(time.RFC3339),
		"updated_at":  agent.UpdatedAt.Format(time.RFC3339),
	}
	if agent.LastActiveAt != nil {
		s := agent.LastActiveAt.Format(time.RFC3339)
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
	var project models.Project
	err = h.pool.QueryRow(r.Context(), `SELECT id, org_id FROM projects WHERE id = $1`, body.ProjectID).Scan(&project.ID, &project.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}

	var ownerOrgRole string
	_ = h.pool.QueryRow(r.Context(),
		`SELECT role FROM org_memberships WHERE user_id = $1 AND org_id = $2`, ownerID, project.OrgID,
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
