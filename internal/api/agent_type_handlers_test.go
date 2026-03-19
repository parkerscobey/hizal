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

func TestCreateAndListAgentTypes(t *testing.T) {
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
	orgMembershipID := uuid.NewString()
	email := "agent-type-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "agent-type-test-org-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM agent_types WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Agent Type Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Agent Type Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (id, user_id, org_id, role) VALUES ($1, $2, $3, 'admin')`, orgMembershipID, userID, orgID); err != nil {
		t.Fatalf("insert org membership: %v", err)
	}

	t.Run("ListAgentTypes includes global presets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/orgs/"+orgID+"/agent-types", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentTypeHandlers(pool).ListAgentTypes(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var body struct {
			AgentTypes []struct {
				ID    string  `json:"id"`
				OrgID *string `json:"org_id"`
				Name  string  `json:"name"`
				Slug  string  `json:"slug"`
			} `json:"agent_types"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		globalTypes := 0
		for _, at := range body.AgentTypes {
			if at.OrgID == nil {
				globalTypes++
			}
		}
		if globalTypes == 0 {
			t.Fatalf("expected global agent types in response, got none")
		}
	})

	t.Run("CreateAgentType creates org-specific type", func(t *testing.T) {
		body := `{"name": "Custom Agent", "slug": "custom-agent", "description": "A custom agent type", "inject_filters": {"include_scopes": ["AGENT", "PROJECT"]}, "search_filters": {"include_scopes": ["AGENT", "PROJECT", "ORG"]}}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agent-types", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentTypeHandlers(pool).CreateAgentType(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var created struct {
			ID          string `json:"id"`
			OrgID       string `json:"org_id"`
			Name        string `json:"name"`
			Slug        string `json:"slug"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if created.OrgID != orgID {
			t.Fatalf("org_id = %q, want %q", created.OrgID, orgID)
		}
		if created.Slug != "custom-agent" {
			t.Fatalf("slug = %q, want %q", created.Slug, "custom-agent")
		}
	})

	t.Run("CreateAgentType rejects duplicate slug", func(t *testing.T) {
		body := `{"name": "Another Custom", "slug": "custom-agent"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agent-types", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentTypeHandlers(pool).CreateAgentType(rr, req)

		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusConflict, rr.Body.String())
		}
	})
}

func TestGlobalAgentTypesCannotBeModified(t *testing.T) {
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

	var globalTypeID string
	if err := pool.QueryRow(ctx, `SELECT id FROM agent_types WHERE org_id IS NULL LIMIT 1`).Scan(&globalTypeID); err != nil {
		t.Skip("no global agent types exist")
	}

	orgID := uuid.NewString()
	userID := uuid.NewString()
	orgMembershipID := uuid.NewString()
	email := "global-type-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "global-type-test-org-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Global Type Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Global Type Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (id, user_id, org_id, role) VALUES ($1, $2, $3, 'admin')`, orgMembershipID, userID, orgID); err != nil {
		t.Fatalf("insert org membership: %v", err)
	}

	t.Run("UpdateAgentType rejects global type", func(t *testing.T) {
		body := `{"name": "Modified Name"}`
		req := httptest.NewRequest(http.MethodPatch, "/v1/agent-types/"+globalTypeID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", globalTypeID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentTypeHandlers(pool).UpdateAgentType(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})

	t.Run("DeleteAgentType rejects global type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/agent-types/"+globalTypeID, nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", globalTypeID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentTypeHandlers(pool).DeleteAgentType(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})
}
