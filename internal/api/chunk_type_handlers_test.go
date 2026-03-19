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

func TestCreateAndListChunkTypes(t *testing.T) {
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
	email := "chunk-type-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "chunk-type-test-org-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chunk_types WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Chunk Type Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Chunk Type Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (id, user_id, org_id, role) VALUES ($1, $2, $3, 'admin')`, orgMembershipID, userID, orgID); err != nil {
		t.Fatalf("insert org membership: %v", err)
	}

	t.Run("ListChunkTypes includes global presets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/orgs/"+orgID+"/chunk-types", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewChunkTypeHandlers(pool).ListChunkTypes(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var body struct {
			ChunkTypes []struct {
				ID     string  `json:"id"`
				OrgID  *string `json:"org_id"`
				Name   string  `json:"name"`
				Slug   string  `json:"slug"`
				Scope  string  `json:"default_scope"`
				Inject bool    `json:"default_always_inject"`
				Consol string  `json:"consolidation_behavior"`
			} `json:"chunk_types"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		globalTypes := 0
		for _, ct := range body.ChunkTypes {
			if ct.OrgID == nil {
				globalTypes++
			}
		}
		if globalTypes == 0 {
			t.Fatalf("expected global chunk types in response, got none")
		}
	})

	t.Run("CreateChunkType creates org-specific type", func(t *testing.T) {
		body := `{"name": "Custom Chunk", "slug": "custom-chunk", "description": "A custom chunk type", "default_scope": "PROJECT", "default_always_inject": false, "consolidation_behavior": "SURFACE"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/chunk-types", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewChunkTypeHandlers(pool).CreateChunkType(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var created struct {
			ID      string `json:"id"`
			OrgID   string `json:"org_id"`
			Name    string `json:"name"`
			Slug    string `json:"slug"`
			Scope   string `json:"default_scope"`
			Consol  string `json:"consolidation_behavior"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if created.OrgID != orgID {
			t.Fatalf("org_id = %q, want %q", created.OrgID, orgID)
		}
		if created.Slug != "custom-chunk" {
			t.Fatalf("slug = %q, want %q", created.Slug, "custom-chunk")
		}
		if created.Consol != "SURFACE" {
			t.Fatalf("consolidation_behavior = %q, want %q", created.Consol, "SURFACE")
		}
	})

	t.Run("CreateChunkType rejects duplicate slug", func(t *testing.T) {
		body := `{"name": "Another Custom", "slug": "custom-chunk"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+orgID+"/chunk-types", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", orgID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewChunkTypeHandlers(pool).CreateChunkType(rr, req)

		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusConflict, rr.Body.String())
		}
	})
}

func TestGlobalChunkTypesCannotBeModified(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `SELECT id FROM chunk_types WHERE org_id IS NULL LIMIT 1`).Scan(&globalTypeID); err != nil {
		t.Skip("no global chunk types exist")
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

	t.Run("UpdateChunkType rejects global type", func(t *testing.T) {
		body := `{"name": "Modified Name"}`
		req := httptest.NewRequest(http.MethodPatch, "/v1/chunk-types/"+globalTypeID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", globalTypeID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewChunkTypeHandlers(pool).UpdateChunkType(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})

	t.Run("DeleteChunkType rejects global type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/chunk-types/"+globalTypeID, nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", globalTypeID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(withJWTUser(req.Context(), JWTUser{ID: userID, Email: email}))

		rr := httptest.NewRecorder()
		NewChunkTypeHandlers(pool).DeleteChunkType(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})
}
