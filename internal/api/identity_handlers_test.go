package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/XferOps/hizal/internal/mcp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGetIdentityHandlerUsesAuthenticatedAgent(t *testing.T) {
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
	orgSlug := "api-get-identity-org-" + strings.ToLower(uuid.NewString())
	userID := uuid.NewString()
	agentID := uuid.NewString()
	spoofedAgentID := uuid.NewString()
	agentSlug := "api-get-identity-agent-" + strings.ToLower(uuid.NewString())
	spoofedAgentSlug := "api-get-identity-spoofed-agent-" + strings.ToLower(uuid.NewString())
	identityID := uuid.NewString()
	spoofedIdentityID := uuid.NewString()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE id = ANY($1::uuid[])`, []string{identityID, spoofedIdentityID})
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE id = ANY($1::uuid[])`, []string{agentID, spoofedAgentID})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "API Get Identity Test Org", orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, "api-get-identity-"+uuid.NewString()+"@example.com", "API Get Identity Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, status)
		VALUES ($1, $2, $3, $4, $5, 'CODER', 'ACTIVE'), ($6, $2, $3, $7, $8, 'CODER', 'ACTIVE')
	`, agentID, orgID, userID, "API Get Identity Test Agent", agentSlug, spoofedAgentID, "Spoofed API Get Identity Test Agent", spoofedAgentSlug); err != nil {
		t.Fatalf("insert agents: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, agent_id, org_id, scope, chunk_type, query_key, title, content, source_lines, gotchas, related)
		VALUES
			($1, NULL, $2, NULL, 'AGENT', 'IDENTITY', 'api-agent-identity', 'API Agent Identity', $3::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb),
			($4, NULL, $5, NULL, 'AGENT', 'IDENTITY', 'spoofed-agent-identity', 'Spoofed Agent Identity', $6::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, identityID, agentID, `"api agent identity"`, spoofedIdentityID, spoofedAgentID, `"spoofed agent identity"`); err != nil {
		t.Fatalf("insert chunks: %v", err)
	}

	h := NewHandlers(mcp.NewTools(pool, nil), pool)
	req := httptest.NewRequest(http.MethodGet, "/v1/context/identity?agent_id="+spoofedAgentID, nil)
	req = req.WithContext(withClaims(req.Context(), AuthClaims{OrgID: orgID, AgentID: agentID}))
	rec := httptest.NewRecorder()

	h.GetIdentity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result mcp.GetIdentityResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Total != 1 || len(result.Chunks) != 1 {
		t.Fatalf("got total=%d len=%d, want one identity", result.Total, len(result.Chunks))
	}
	if result.Chunks[0].ID != identityID {
		t.Fatalf("returned identity %q, want authenticated agent identity %q", result.Chunks[0].ID, identityID)
	}
}
