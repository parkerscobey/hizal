package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRateLimit_Allow(t *testing.T) {
	bucket := newTokenBucket(10, 10)

	for i := 0; i < 10; i++ {
		if !bucket.allow() {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	if bucket.allow() {
		t.Error("request 11 should be denied (bucket exhausted)")
	}
}

func TestRateLimit_Refill(t *testing.T) {
	bucket := newTokenBucket(1000, 1)

	if !bucket.allow() {
		t.Fatal("first request should be allowed")
	}

	if bucket.allow() {
		t.Error("second request should be denied (bucket empty)")
	}
}

func TestPublicChunks_ListOnlyReturnsPublic(t *testing.T) {
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
	projectID := uuid.NewString()
	userID := uuid.NewString()
	email := "pub-chunks-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "pub-chunks-test-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, orgID, "Public Chunks Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Public Chunks Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Public Chunks Test Project", orgSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	publicChunkID := uuid.NewString()
	privateChunkID := uuid.NewString()

	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, 'PROJECT', 'KNOWLEDGE', 'public-chunk', 'Public Chunk Title', '"public content"'::jsonb, 'public')
	`, publicChunkID, projectID); err != nil {
		t.Fatalf("insert public chunk: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, 'PROJECT', 'KNOWLEDGE', 'private-chunk', 'Private Chunk Title', '"private content"'::jsonb, 'private')
	`, privateChunkID, projectID); err != nil {
		t.Fatalf("insert private chunk: %v", err)
	}

	router := NewRouter(pool, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/chunks", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var response struct {
		Chunks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for _, chunk := range response.Chunks {
		if chunk.ID == privateChunkID {
			t.Error("private chunk should not appear in public chunks list")
		}
	}
}

func TestPublicChunks_FilterByType(t *testing.T) {
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
	projectID := uuid.NewString()
	userID := uuid.NewString()
	email := "pub-chunks-type-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "pub-chunks-type-test-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, orgID, "Public Chunks Type Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Public Chunks Type Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Public Chunks Type Test Project", orgSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	knowledgeChunkID := uuid.NewString()
	conventionChunkID := uuid.NewString()

	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, 'PROJECT', 'KNOWLEDGE', 'knowledge-chunk', 'Knowledge Chunk', '"knowledge content"'::jsonb, 'public')
	`, knowledgeChunkID, projectID); err != nil {
		t.Fatalf("insert knowledge chunk: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, 'PROJECT', 'CONVENTION', 'convention-chunk', 'Convention Chunk', '"convention content"'::jsonb, 'public')
	`, conventionChunkID, projectID); err != nil {
		t.Fatalf("insert convention chunk: %v", err)
	}

	router := NewRouter(pool, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/chunks?type=CONVENTION", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var response struct {
		Chunks []struct {
			ID        string `json:"id"`
			ChunkType string `json:"chunk_type"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for _, chunk := range response.Chunks {
		if chunk.ChunkType != "CONVENTION" {
			t.Errorf("chunk type = %s, want CONVENTION", chunk.ChunkType)
		}
		if chunk.ID == knowledgeChunkID {
			t.Error("KNOWLEDGE chunk should not appear when filtering by CONVENTION")
		}
	}
}

func TestGetPublicChunk_NotFound(t *testing.T) {
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

	router := NewRouter(pool, nil)
	nonExistentID := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/chunks/"+nonExistentID, nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

func TestGetPublicChunk_PrivateReturns404(t *testing.T) {
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
	projectID := uuid.NewString()
	userID := uuid.NewString()
	email := "pub-chunks-private-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "pub-chunks-private-test-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, orgID, "Public Chunks Private Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Public Chunks Private Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Public Chunks Private Test Project", orgSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	privateChunkID := uuid.NewString()

	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, 'PROJECT', 'KNOWLEDGE', 'private-chunk', 'Private Chunk Title', '"private content"'::jsonb, 'private')
	`, privateChunkID, projectID); err != nil {
		t.Fatalf("insert private chunk: %v", err)
	}

	router := NewRouter(pool, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/chunks/"+privateChunkID, nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

func TestPublicChunks_SearchFallback(t *testing.T) {
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
	projectID := uuid.NewString()
	userID := uuid.NewString()
	email := "pub-chunks-search-test-" + strings.ToLower(uuid.NewString()) + "@example.com"
	orgSlug := "pub-chunks-search-test-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, orgID, "Public Chunks Search Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, email, "Public Chunks Search Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Public Chunks Search Test Project", orgSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	searchableChunkID := uuid.NewString()

	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, 'PROJECT', 'KNOWLEDGE', 'search-chunk', 'Searchable Chunk', '"searchable content for testing"'::jsonb, 'public')
	`, searchableChunkID, projectID); err != nil {
		t.Fatalf("insert searchable chunk: %v", err)
	}

	router := NewRouter(pool, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/chunks/search?q=searchable", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var response struct {
		Chunks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	found := false
	for _, chunk := range response.Chunks {
		if chunk.ID == searchableChunkID {
			found = true
			break
		}
	}
	if !found {
		t.Error("searchable chunk should appear in search results")
	}
}
