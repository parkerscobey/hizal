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

func TestGetSessionConsolidationChunks(t *testing.T) {
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
	sessionID := uuid.NewString()
	projectID := uuid.NewString()
	orgSlug := "consolidation-test-" + strings.ToLower(uuid.NewString())
	agentSlug := "agent-" + strings.ToLower(uuid.NewString())
	projectSlug := "proj-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM context_chunks WHERE agent_id = $1`, agentID)
		pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
		pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, agentID)
		pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Consolidation Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, "consolidation-test-"+uuid.NewString()+"@example.com", "Consolidation Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Test Project", projectSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, status)
		VALUES ($1, $2, $3, $4, $5, 'CODER', 'ACTIVE')
	`, agentID, orgID, userID, "Test Agent", agentSlug); err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	sessionStartedAt := "2026-03-19T10:00:00Z"
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (id, agent_id, org_id, project_id, status, focus_task, started_at, expires_at, chunks_written, chunks_read)
		VALUES ($1, $2, $3, $4, 'ended', 'Test consolidation session', $5, $6, 3, 0)
	`, sessionID, agentID, orgID, projectID, sessionStartedAt, "2026-03-20T10:00:00Z"); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	type chunk struct {
		id     string
		slug   string
		scope  string
		inject bool
	}
	chunks := []chunk{
		{uuid.NewString(), "MEMORY", "AGENT", false},
		{uuid.NewString(), "RESEARCH", "AGENT", false},
		{uuid.NewString(), "KNOWLEDGE", "PROJECT", false},
		{uuid.NewString(), "CONVENTION", "PROJECT", true},
	}
	for _, c := range chunks {
		var injectVal interface{}
		if c.inject {
			injectVal = `{"rules":[{"all":true}]}`
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO context_chunks (id, agent_id, org_id, project_id, query_key, title, scope, chunk_type, inject_audience, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, c.id, agentID, orgID, projectID, "test-key-"+c.slug, "Test "+c.slug, c.scope, c.slug, injectVal, sessionStartedAt); err != nil {
			t.Fatalf("insert chunk %s: %v", c.slug, err)
		}
	}
	for _, c := range chunks {
		t.Cleanup(func() {
			pool.Exec(ctx, `DELETE FROM context_chunks WHERE id = $1`, c.id)
		})
	}

	t.Run("Returns only SURFACE chunks", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/consolidation-chunks", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", sessionID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withClaims(req.Context(), AuthClaims{OrgID: orgID}))

		rr := httptest.NewRecorder()
		NewSessionHandlers(nil, pool).GetSessionConsolidationChunks(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var body struct {
			SessionID string `json:"session_id"`
			Chunks    []struct {
				ID        string `json:"id"`
				QueryKey  string `json:"query_key"`
				Title     string `json:"title"`
				Scope     string `json:"scope"`
				ChunkType string `json:"chunk_type"`
			} `json:"chunks"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if body.SessionID != sessionID {
			t.Errorf("session_id = %q, want %q", body.SessionID, sessionID)
		}
		if body.Total != 2 {
			t.Errorf("total = %d, want 2 (MEMORY and RESEARCH are SURFACE; KNOWLEDGE and CONVENTION are KEEP)", body.Total)
		}

		surfaceTypes := map[string]bool{}
		for _, c := range body.Chunks {
			surfaceTypes[c.ChunkType] = true
		}
		if !surfaceTypes["MEMORY"] {
			t.Error("expected MEMORY chunk in SURFACE results")
		}
		if !surfaceTypes["RESEARCH"] {
			t.Error("expected RESEARCH chunk in SURFACE results")
		}
		if surfaceTypes["KNOWLEDGE"] {
			t.Error("KNOWLEDGE chunk should not be in SURFACE results (it is KEEP type)")
		}
		if surfaceTypes["CONVENTION"] {
			t.Error("CONVENTION chunk should not be in SURFACE results (it is KEEP type)")
		}
	})

	t.Run("MemoryChunks alias returns same results", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/memory-chunks", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", sessionID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withClaims(req.Context(), AuthClaims{OrgID: orgID}))

		rr := httptest.NewRecorder()
		NewSessionHandlers(nil, pool).GetSessionMemoryChunks(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var body struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if body.Total != 2 {
			t.Errorf("total = %d, want 2 (same as /consolidation-chunks)", body.Total)
		}
	})
}
