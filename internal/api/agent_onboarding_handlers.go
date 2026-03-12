package api

import (
	"net/http"

	"github.com/XferOps/winnow/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentOnboardingHandlers struct {
	pool *pgxpool.Pool
}

func NewAgentOnboardingHandlers(pool *pgxpool.Pool) *AgentOnboardingHandlers {
	return &AgentOnboardingHandlers{pool: pool}
}

type onboardingProject struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Selected bool   `json:"selected"`
}

func (h *AgentOnboardingHandlers) buildProjects(r *http.Request, key models.APIKey, selectedProjectID string) ([]onboardingProject, error) {
	projectRows, err := h.pool.Query(r.Context(), `
		SELECT p.id, p.name, p.slug
		FROM projects p
		WHERE p.id = ANY(
			CASE
				WHEN $1 = 'AGENT' THEN COALESCE(ARRAY(
					SELECT ap.project_id
					FROM agent_projects ap
					WHERE ap.agent_id = $2
				), '{}'::uuid[])
				WHEN $3 THEN COALESCE(ARRAY(
					SELECT p2.id
					FROM projects p2
					WHERE p2.org_id = $4
				), '{}'::uuid[])
				ELSE COALESCE($5::uuid[], '{}'::uuid[])
			END
		)
		ORDER BY p.created_at
	`, key.OwnerType, key.AgentID, key.ScopeAllProjects, key.OrgID, key.AllowedProjectIDs)
	if err != nil {
		return nil, err
	}
	defer projectRows.Close()

	projects := make([]onboardingProject, 0)
	for projectRows.Next() {
		var project models.Project
		if err := projectRows.Scan(&project.ID, &project.Name, &project.Slug); err != nil {
			continue
		}
		projects = append(projects, onboardingProject{
			ID:       project.ID,
			Name:     project.Name,
			Slug:     project.Slug,
			Selected: project.ID == selectedProjectID,
		})
	}
	return projects, nil
}

func (h *AgentOnboardingHandlers) buildResponse(key models.APIKey, org models.Org, agentName, agentSlug, ownerName *string, projects []onboardingProject, selectedProjectID string) map[string]interface{} {
	var defaultProjectID *string
	if len(projects) == 1 {
		defaultProjectID = &projects[0].ID
	}

	var selectedProjectIDPtr *string
	if selectedProjectID != "" {
		selectedProjectIDPtr = &selectedProjectID
	}

	needsProjectSelection := len(projects) > 1 && selectedProjectIDPtr == nil

	instructions := []string{
		"Use Winnow before exploring the codebase directly. Search existing context first, then read the top chunks.",
		"If context is missing, stale, or incomplete, inspect the codebase and write or update structured chunks with concrete file references and gotchas.",
		"Before handoff or after a long working session, use compact_context and write back a summary chunk for the next agent.",
		"After relying on context to complete work, submit a review with usefulness, correctness, and an action.",
	}
	if needsProjectSelection {
		instructions = append(instructions,
			"Choose one project from available_projects and send X-Project-ID on subsequent MCP or context requests.")
	} else if selectedProjectIDPtr != nil {
		instructions = append(instructions,
			"Continue using X-Project-ID="+*selectedProjectIDPtr+" on subsequent MCP or context requests.")
	} else if defaultProjectID != nil {
		instructions = append(instructions,
			"This API key or agent currently has one available project. Use X-Project-ID="+*defaultProjectID+" on subsequent MCP or context requests.")
	}

	startQueries := []string{
		"project overview architecture",
		"authentication authorization",
		"data model migrations",
		"deployment environment configuration",
		"recent changes roadmap",
	}

	return map[string]interface{}{
		"application":    "winnow",
		"version":        version,
		"guide_markdown": agentOnboardingGuideMarkdown,
		"key": map[string]interface{}{
			"id":                 key.ID,
			"name":               key.Name,
			"owner_type":         key.OwnerType,
			"scope_all_projects": key.ScopeAllProjects,
		},
		"org": map[string]interface{}{
			"id":   key.OrgID,
			"name": org.Name,
			"slug": org.Slug,
		},
		"agent": map[string]interface{}{
			"id":   key.AgentID,
			"name": agentName,
			"slug": agentSlug,
		},
		"owner": map[string]interface{}{
			"user_id": key.UserID,
			"name":    ownerName,
		},
		"default_project_id":        defaultProjectID,
		"selected_project_id":       selectedProjectIDPtr,
		"needs_project_selection":   needsProjectSelection,
		"available_projects":        projects,
		"mcp_endpoint":              "/mcp",
		"context_api_base":          "/v1/context",
		"recommended_start_queries": startQueries,
		"tooling": map[string]interface{}{
			"implemented_tools": []string{
				"search_context",
				"read_context",
				"write_context",
				"update_context",
				"get_context_versions",
				"compact_context",
				"review_context",
				"delete_context",
			},
			"required_headers": []string{
				"Authorization: Bearer <api-key>",
				"X-Project-ID: <project-id>",
			},
		},
		"instructions": instructions,
		"chunk_shape": map[string]interface{}{
			"required": []string{"query_key", "title", "content"},
			"optional": []string{"source_file", "source_lines", "gotchas", "related"},
		},
	}
}

// GET /api/v1/agent-onboarding
func (h *AgentOnboardingHandlers) Get(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFrom(r.Context())
	if !ok || claims.KeyID == "" {
		writeError(w, http.StatusUnauthorized, "AUTH_INVALID", "no API key claims")
		return
	}
	if h.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
		return
	}

	var key models.APIKey
	var org models.Org
	var agentName *string
	var agentSlug *string
	var ownerName *string

	err := h.pool.QueryRow(r.Context(), `
		SELECT
			ak.id,
			ak.owner_type,
			ak.user_id,
			ak.agent_id,
			ak.org_id,
			ak.name,
			ak.scope_all_projects,
			ak.allowed_project_ids,
			o.name,
			o.slug,
			a.name,
			a.slug,
			u.name
		FROM api_keys ak
		JOIN orgs o ON o.id = ak.org_id
		LEFT JOIN agents a ON a.id = ak.agent_id
		LEFT JOIN users u ON u.id = ak.user_id
		WHERE ak.id = $1
	`, claims.KeyID).Scan(
		&key.ID,
		&key.OwnerType,
		&key.UserID,
		&key.AgentID,
		&key.OrgID,
		&key.Name,
		&key.ScopeAllProjects,
		&key.AllowedProjectIDs,
		&org.Name,
		&org.Slug,
		&agentName,
		&agentSlug,
		&ownerName,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to load API key metadata")
		return
	}

	projects, err := h.buildProjects(r, key, claims.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to load projects for API key")
		return
	}

	writeJSON(w, http.StatusOK, h.buildResponse(key, org, agentName, agentSlug, ownerName, projects, claims.ProjectID))
}

// GET /api/v1/agents/{id}/onboarding
func (h *AgentOnboardingHandlers) GetForAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	orgID, ownerID, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin", "member", "viewer")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	if h.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
		return
	}

	var agent models.Agent
	var org models.Org
	var owner models.User
	if err := h.pool.QueryRow(r.Context(), `
		SELECT a.id, a.org_id, a.owner_id, a.name, a.slug, o.name, o.slug, u.name
		FROM agents a
		JOIN orgs o ON o.id = a.org_id
		JOIN users u ON u.id = a.owner_id
		WHERE a.id = $1
	`, agentID).Scan(&agent.ID, &agent.OrgID, &agent.OwnerID, &agent.Name, &agent.Slug, &org.Name, &org.Slug, &owner.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to load agent metadata")
		return
	}

	key := models.APIKey{
		OwnerType:         "AGENT",
		AgentID:           &agent.ID,
		OrgID:             &orgID,
		UserID:            &ownerID,
		Name:              agent.Name,
		ScopeAllProjects:  false,
		AllowedProjectIDs: []string{},
	}

	selectedProjectID := r.URL.Query().Get("project_id")
	if selectedProjectID == "" {
		selectedProjectID = r.Header.Get("X-Project-ID")
	}

	projects, err := h.buildProjects(r, key, selectedProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to load projects for agent")
		return
	}

	writeJSON(w, http.StatusOK, h.buildResponse(key, org, &agent.Name, &agent.Slug, &owner.Name, projects, selectedProjectID))
}
