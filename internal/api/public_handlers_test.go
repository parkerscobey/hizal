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
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'public-chunk', 'Public Chunk Title', '"public content"'::jsonb, 'public')
	`, publicChunkID, projectID, orgID); err != nil {
		t.Fatalf("insert public chunk: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'private-chunk', 'Private Chunk Title', '"private content"'::jsonb, 'private')
	`, privateChunkID, projectID, orgID); err != nil {
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
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'knowledge-chunk', 'Knowledge Chunk', '"knowledge content"'::jsonb, 'public')
	`, knowledgeChunkID, projectID, orgID); err != nil {
		t.Fatalf("insert knowledge chunk: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'CONVENTION', 'convention-chunk', 'Convention Chunk', '"convention content"'::jsonb, 'public')
	`, conventionChunkID, projectID, orgID); err != nil {
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
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'private-chunk', 'Private Chunk Title', '"private content"'::jsonb, 'private')
	`, privateChunkID, projectID, orgID); err != nil {
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
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'search-chunk', 'Searchable Chunk', '"searchable content for testing"'::jsonb, 'public')
	`, searchableChunkID, projectID, orgID); err != nil {
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

func TestAddPublicChunk_ToProject(t *testing.T) {
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

	sourceOrgID := uuid.NewString()
	sourceProjectID := uuid.NewString()
	userID := uuid.NewString()
	destOrgID := uuid.NewString()
	destProjectID := uuid.NewString()
	userEmail := "add-chunk-test-" + strings.ToLower(uuid.NewString()) + "@example.com"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1 OR org_id = $2`, sourceOrgID, destOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1 OR id = $2`, sourceProjectID, destProjectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1 OR id = $2`, sourceOrgID, destOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, sourceOrgID, "Source Org", "source-org-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert source org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, destOrgID, "Dest Org", "dest-org-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert dest org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, userEmail, "Add Chunk Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1, $2, 'member')`, userID, destOrgID); err != nil {
		t.Fatalf("insert org membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, sourceProjectID, sourceOrgID, "Source Project", "source-proj-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert source project: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, destProjectID, destOrgID, "Dest Project", "dest-proj-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert dest project: %v", err)
	}

	publicChunkID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'add-chunk-test', 'Public Chunk', '"public content"'::jsonb, 'public')
	`, publicChunkID, sourceProjectID, sourceOrgID); err != nil {
		t.Fatalf("insert public chunk: %v", err)
	}

	token, err := SignJWT(userID, userEmail)
	if err != nil {
		t.Fatalf("SignJWT() error = %v", err)
	}

	router := NewRouter(pool, nil)

	t.Run("add to project", func(t *testing.T) {
		body := map[string]interface{}{
			"scope":      "PROJECT",
			"project_id": destProjectID,
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/public/chunks/"+publicChunkID+"/add", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var response map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		newChunkID := response["id"]
		if newChunkID == "" {
			t.Fatal("response should contain new chunk ID")
		}

		var sourceChunkID, visibility, sourceOrgName string
		err := pool.QueryRow(ctx, `
			SELECT source_chunk_id, visibility, COALESCE(source_org_name, '')
			FROM context_chunks WHERE id = $1
		`, newChunkID).Scan(&sourceChunkID, &visibility, &sourceOrgName)
		if err != nil {
			t.Fatalf("query new chunk: %v", err)
		}

		if sourceChunkID != publicChunkID {
			t.Errorf("source_chunk_id = %s, want %s", sourceChunkID, publicChunkID)
		}
		if visibility != "private" {
			t.Errorf("visibility = %s, want private", visibility)
		}
		if sourceOrgName == "" {
			t.Error("source_org_name should be set")
		}
	})

	t.Run("not found for non-public chunk", func(t *testing.T) {
		privateChunkID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
			VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'private-chunk', 'Private Chunk', '"private content"'::jsonb, 'private')
		`, privateChunkID, sourceProjectID, sourceOrgID); err != nil {
			t.Fatalf("insert private chunk: %v", err)
		}

		body := map[string]interface{}{
			"scope":      "PROJECT",
			"project_id": destProjectID,
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/public/chunks/"+privateChunkID+"/add", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
		}
	})

	t.Run("missing scope", func(t *testing.T) {
		body := map[string]interface{}{
			"project_id": destProjectID,
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/public/chunks/"+publicChunkID+"/add", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
		}
	})

	t.Run("missing project_id for PROJECT scope", func(t *testing.T) {
		body := map[string]interface{}{
			"scope": "PROJECT",
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/public/chunks/"+publicChunkID+"/add", strings.NewReader(string(bodyBytes)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
		}
	})
}

func TestAddPublicChunk_ToOrg(t *testing.T) {
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

	sourceOrgID := uuid.NewString()
	sourceProjectID := uuid.NewString()
	userID := uuid.NewString()
	destOrgID := uuid.NewString()
	userEmail := "add-chunk-org-test-" + strings.ToLower(uuid.NewString()) + "@example.com"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1 OR org_id = $2`, sourceOrgID, destOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, sourceProjectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1 OR id = $2`, sourceOrgID, destOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, sourceOrgID, "Source Org", "source-org-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert source org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, destOrgID, "Dest Org", "dest-org-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert dest org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, userEmail, "Add Chunk Org Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1, $2, 'admin')`, userID, destOrgID); err != nil {
		t.Fatalf("insert org admin membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, sourceProjectID, sourceOrgID, "Source Project", "source-proj-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert source project: %v", err)
	}

	publicChunkID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'add-chunk-org-test', 'Public Chunk', '"public content"'::jsonb, 'public')
	`, publicChunkID, sourceProjectID, sourceOrgID); err != nil {
		t.Fatalf("insert public chunk: %v", err)
	}

	token, err := SignJWT(userID, userEmail)
	if err != nil {
		t.Fatalf("SignJWT() error = %v", err)
	}

	router := NewRouter(pool, nil)

	body := map[string]interface{}{
		"scope":  "ORG",
		"org_id": destOrgID,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/chunks/"+publicChunkID+"/add", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	newChunkID := response["id"]
	if newChunkID == "" {
		t.Fatal("response should contain new chunk ID")
	}

	var scope, visibility string
	err = pool.QueryRow(ctx, `
		SELECT scope, visibility FROM context_chunks WHERE id = $1
	`, newChunkID).Scan(&scope, &visibility)
	if err != nil {
		t.Fatalf("query new chunk: %v", err)
	}

	if scope != "ORG" {
		t.Errorf("scope = %s, want ORG", scope)
	}
	if visibility != "private" {
		t.Errorf("visibility = %s, want private", visibility)
	}
}

func TestAddPublicChunk_ToAgent(t *testing.T) {
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

	sourceOrgID := uuid.NewString()
	sourceProjectID := uuid.NewString()
	userID := uuid.NewString()
	destOrgID := uuid.NewString()
	agentID := uuid.NewString()
	userEmail := "add-chunk-agent-test-" + strings.ToLower(uuid.NewString()) + "@example.com"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE org_id = $1 OR org_id = $2`, sourceOrgID, destOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, agentID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, sourceProjectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1 OR id = $2`, sourceOrgID, destOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, sourceOrgID, "Source Org", "source-org-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert source org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug, tier) VALUES ($1, $2, $3, 'pro')`, destOrgID, "Dest Org", "dest-org-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert dest org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, userEmail, "Add Chunk Agent Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agents (id, org_id, owner_id, name, slug, status) VALUES ($1, $2, $3, $4, $5, 'active')`, agentID, destOrgID, userID, "Test Agent", "test-agent-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, sourceProjectID, sourceOrgID, "Source Project", "source-proj-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert source project: %v", err)
	}

	publicChunkID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, org_id, scope, chunk_type, query_key, title, content, visibility)
		VALUES ($1, $2, $3, 'PROJECT', 'KNOWLEDGE', 'add-chunk-agent-test', 'Public Chunk', '"public content"'::jsonb, 'public')
	`, publicChunkID, sourceProjectID, sourceOrgID); err != nil {
		t.Fatalf("insert public chunk: %v", err)
	}

	token, err := SignJWT(userID, userEmail)
	if err != nil {
		t.Fatalf("SignJWT() error = %v", err)
	}

	router := NewRouter(pool, nil)

	body := map[string]interface{}{
		"scope":   "AGENT",
		"org_id":  destOrgID,
		"agent_id": agentID,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/chunks/"+publicChunkID+"/add", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	newChunkID := response["id"]
	if newChunkID == "" {
		t.Fatal("response should contain new chunk ID")
	}

	var scope, visibility string
	err = pool.QueryRow(ctx, `
		SELECT scope, visibility FROM context_chunks WHERE id = $1
	`, newChunkID).Scan(&scope, &visibility)
	if err != nil {
		t.Fatalf("query new chunk: %v", err)
	}

	if scope != "AGENT" {
		t.Errorf("scope = %s, want AGENT", scope)
	}
	if visibility != "private" {
		t.Errorf("visibility = %s, want private", visibility)
	}
}
