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

func TestCreateAgentWithTypeID(t *testing.T) {
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
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE org_id = $1`, orgID)
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

	t.Run("CreateAgent with type_id uses global preset", func(t *testing.T) {
		var globalTypeID string
		if err := pool.QueryRow(ctx, `SELECT id FROM agent_types WHERE org_id IS NULL LIMIT 1`).Scan(&globalTypeID); err != nil {
			t.Skip("no global agent types exist")
		}

		body := `{"name": "Test Agent", "slug": "test-agent-global", "type_id": "` + globalTypeID + `"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentHandlers(pool).CreateAgent(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var created struct {
			ID     string  `json:"id"`
			Type   string  `json:"type"`
			TypeID *string `json:"type_id"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if created.TypeID == nil || *created.TypeID != globalTypeID {
			t.Fatalf("type_id = %v, want %q", created.TypeID, globalTypeID)
		}
		if created.Type == "" {
			t.Fatalf("type should be derived from agent_type slug, got empty")
		}
	})

	t.Run("CreateAgent with invalid type_id returns error", func(t *testing.T) {
		body := `{"name": "Test Agent", "slug": "test-agent-invalid", "type_id": "` + uuid.NewString() + `"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentHandlers(pool).CreateAgent(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
		}
	})

	t.Run("CreateAgent with type string falls back to validAgentTypes", func(t *testing.T) {
		body := `{"name": "Test Agent Type", "slug": "test-agent-type-fallback", "type": "CODER"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentHandlers(pool).CreateAgent(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var created struct {
			ID     string  `json:"id"`
			Type   string  `json:"type"`
			TypeID *string `json:"type_id"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if created.Type != "CODER" {
			t.Fatalf("type = %q, want %q", created.Type, "CODER")
		}
		if created.TypeID != nil {
			t.Fatalf("type_id should be nil when using type string fallback, got %v", created.TypeID)
		}
	})

	t.Run("CreateAgent with invalid type string returns error", func(t *testing.T) {
		body := `{"name": "Test Agent", "slug": "test-agent-bad-type", "type": "INVALID_TYPE"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentHandlers(pool).CreateAgent(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
		}
	})

	t.Run("CreateAgent defaults to CUSTOM when neither type_id nor type provided", func(t *testing.T) {
		body := `{"name": "Test Agent Default", "slug": "test-agent-default"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/agents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewAgentHandlers(pool).CreateAgent(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var created struct {
			ID     string  `json:"id"`
			Type   string  `json:"type"`
			TypeID *string `json:"type_id"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if created.Type != "CUSTOM" {
			t.Fatalf("type = %q, want %q", created.Type, "CUSTOM")
		}
	})
}
