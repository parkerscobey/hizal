package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReadContextByQueryKey_AgentScoped is the reproduction for #138:
// read_context(query_key=...) must find an AGENT-scoped chunk even when a
// project_id is supplied (the MCP client sends one), because an AGENT-scoped
// chunk has project_id NULL and is not addressable by any project.
//
// The bug: the query_key branch of ReadContext uses `WHERE query_key=$1 AND
// project_id=$2` whenever projectID != "", which excludes AGENT/ORG chunks
// (NULL project_id) — the same query_key becomes "chunk not found", an error
// indistinguishable from genuine absence.
func TestReadContextByQueryKey_AgentScoped(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	orgID := uuid.NewString()
	projectID := uuid.NewString()
	agentID := uuid.NewString()
	chunkID := uuid.NewString()
	ownerID := uuid.NewString()
	queryKey := "agent-scoped-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE id=$1`, chunkID)
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE id=$1`, agentID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, ownerID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id=$1`, orgID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id,name,slug) VALUES ($1,$2,$3)`,
		orgID, "Scope Test Org", "scope-org-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,name,password_hash) VALUES ($1,$2,$3,$4)`,
		ownerID, "scope-"+uuid.NewString()+"@test.invalid", "Scope Test Owner", "x"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,slug) VALUES ($1,$2,$3,$4)`,
		projectID, orgID, "Scope Test Project", "scope-proj-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agents (id,org_id,owner_id,name,slug) VALUES ($1,$2,$3,$4,$5)`,
		agentID, orgID, ownerID, "Scope Test Agent", "scope-agent-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	// AGENT-scoped chunk: project_id NULL, agent_id set (see 009_scope).
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, agent_id, chunk_type, query_key, title, content, source_lines, gotchas, related)
		VALUES ($1, NULL, 'AGENT', $2, 'MEMORY', $3, $4, '{"body":"x"}'::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, chunkID, agentID, queryKey, "Agent chunk"); err != nil {
		t.Fatalf("insert agent chunk: %v", err)
	}

	tools := &Tools{pool: pool, embed: nil}

	// The reproduction: a project_id IS supplied (the client sends one), and the
	// chunk is AGENT-scoped. It must still be found by query_key.
	result, err := tools.ReadContext(ctx, projectID, ReadContextInput{QueryKey: queryKey})
	if err != nil {
		t.Fatalf("#138: read_context(query_key) with a project_id could not find an "+
			"AGENT-scoped chunk: %v", err)
	}
	if result.ID != chunkID {
		t.Fatalf("id = %q, want %q", result.ID, chunkID)
	}
	if result.Scope != "AGENT" {
		t.Fatalf("scope = %q, want AGENT", result.Scope)
	}

	// And with no project_id (the other caller shape) it must still resolve.
	if _, err := tools.ReadContext(ctx, "", ReadContextInput{QueryKey: queryKey}); err != nil {
		t.Fatalf("#138: read_context(query_key) with no project_id failed for an "+
			"AGENT-scoped chunk: %v", err)
	}
}
