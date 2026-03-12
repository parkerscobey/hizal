package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAgentOnboardingEndpointReturnsAgentProjects(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping() error = %v", err)
	}

	orgID := uuid.NewString()
	userID := uuid.NewString()
	agentID := uuid.NewString()
	projectID := uuid.NewString()
	orgMembershipID := uuid.NewString()
	apiKeyID := uuid.NewString()
	orgSlug := "agent-onboard-org-" + strings.ToLower(uuid.NewString())
	projectSlug := "agent-onboard-project-" + strings.ToLower(uuid.NewString())
	userEmail := "agent-onboard-" + strings.ToLower(uuid.NewString()) + "@example.com"
	keyHash := "hash-" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Agent Onboarding Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, userEmail, "Agent Owner"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (id, user_id, org_id, role) VALUES ($1, $2, $3, 'member')`, orgMembershipID, userID, orgID); err != nil {
		t.Fatalf("insert org membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Agent Onboarding Project", projectSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, status)
		VALUES ($1, $2, $3, $4, $5, 'CODER', 'ACTIVE')
	`, agentID, orgID, userID, "Code Agent", "code-agent-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agent_projects (agent_id, project_id) VALUES ($1, $2)`, agentID, projectID); err != nil {
		t.Fatalf("insert agent project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (id, owner_type, agent_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ($1, 'AGENT', $2, $3, $4, $5, FALSE, '{}')
	`, apiKeyID, agentID, orgID, keyHash, "agent key"); err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-onboarding", nil)
	req = req.WithContext(withClaims(req.Context(), AuthClaims{
		OrgID:     orgID,
		KeyID:     apiKeyID,
		ProjectID: projectID,
	}))

	rr := httptest.NewRecorder()
	NewAgentOnboardingHandlers(pool).Get(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var body struct {
		GuideMarkdown         string  `json:"guide_markdown"`
		DefaultProjectID      *string `json:"default_project_id"`
		SelectedProjectID     *string `json:"selected_project_id"`
		NeedsProjectSelection bool    `json:"needs_project_selection"`
		AvailableProjects     []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Selected bool   `json:"selected"`
		} `json:"available_projects"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(body.GuideMarkdown, "Winnow Agent Onboarding Guide") {
		t.Fatalf("guide_markdown missing expected heading")
	}

	if body.DefaultProjectID == nil || *body.DefaultProjectID != projectID {
		t.Fatalf("default_project_id = %v, want %q", body.DefaultProjectID, projectID)
	}
	if body.SelectedProjectID == nil || *body.SelectedProjectID != projectID {
		t.Fatalf("selected_project_id = %v, want %q", body.SelectedProjectID, projectID)
	}
	if body.NeedsProjectSelection {
		t.Fatal("needs_project_selection = true, want false")
	}
	if len(body.AvailableProjects) != 1 {
		t.Fatalf("len(available_projects) = %d, want 1", len(body.AvailableProjects))
	}
	if body.AvailableProjects[0].ID != projectID || !body.AvailableProjects[0].Selected {
		t.Fatalf("available_projects[0] = %+v, want selected project", body.AvailableProjects[0])
	}
}

func TestAgentOnboardingJWTEndpointReturnsAgentProjects(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping() error = %v", err)
	}

	orgID := uuid.NewString()
	userID := uuid.NewString()
	agentID := uuid.NewString()
	projectID := uuid.NewString()
	orgMembershipID := uuid.NewString()
	orgSlug := "agent-ui-onboard-org-" + strings.ToLower(uuid.NewString())
	projectSlug := "agent-ui-onboard-project-" + strings.ToLower(uuid.NewString())
	userEmail := "agent-ui-onboard-" + strings.ToLower(uuid.NewString()) + "@example.com"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Agent UI Onboarding Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, userEmail, "Agent Owner"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (id, user_id, org_id, role) VALUES ($1, $2, $3, 'member')`, orgMembershipID, userID, orgID); err != nil {
		t.Fatalf("insert org membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Agent UI Onboarding Project", projectSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, status)
		VALUES ($1, $2, $3, $4, $5, 'CODER', 'ACTIVE')
	`, agentID, orgID, userID, "UI Code Agent", "ui-code-agent-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agent_projects (agent_id, project_id) VALUES ($1, $2)`, agentID, projectID); err != nil {
		t.Fatalf("insert agent project: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/onboarding", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: userEmail}))

	rr := httptest.NewRecorder()
	NewAgentOnboardingHandlers(pool).GetForAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var body struct {
		GuideMarkdown         string  `json:"guide_markdown"`
		DefaultProjectID      *string `json:"default_project_id"`
		SelectedProjectID     *string `json:"selected_project_id"`
		NeedsProjectSelection bool    `json:"needs_project_selection"`
		Agent                 struct {
			ID   *string `json:"id"`
			Name *string `json:"name"`
			Slug *string `json:"slug"`
		} `json:"agent"`
		AvailableProjects []struct {
			ID       string `json:"id"`
			Selected bool   `json:"selected"`
		} `json:"available_projects"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(body.GuideMarkdown, "Winnow Agent Onboarding Guide") {
		t.Fatalf("guide_markdown missing expected heading")
	}
	if body.Agent.ID == nil || *body.Agent.ID != agentID {
		t.Fatalf("agent.id = %v, want %q", body.Agent.ID, agentID)
	}
	if body.DefaultProjectID == nil || *body.DefaultProjectID != projectID {
		t.Fatalf("default_project_id = %v, want %q", body.DefaultProjectID, projectID)
	}
	if body.SelectedProjectID != nil {
		t.Fatalf("selected_project_id = %v, want nil when request is not scoped", body.SelectedProjectID)
	}
	if body.NeedsProjectSelection {
		t.Fatal("needs_project_selection = true, want false")
	}
	if len(body.AvailableProjects) != 1 || body.AvailableProjects[0].ID != projectID || body.AvailableProjects[0].Selected {
		t.Fatalf("available_projects = %+v, want one unselected default project", body.AvailableProjects)
	}
}
