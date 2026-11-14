package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestUpdateContext_AgentScopedByID is the reproduction for #139: update_context
// must accept any id read_context / list_chunks return. For an AGENT-scoped
// chunk (project_id NULL) present with a project_id (as the MCP client sends),
// the final UPDATE's `AND project_id = $M` gate matches zero rows — the update
// silently affects nothing, or the fetch/update pair disagrees.
func TestUpdateContext_AgentScopedByID(t *testing.T) {
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
	queryKey := "upd-agent-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE id=$1`, chunkID)
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE id=$1`, agentID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, ownerID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id=$1`, orgID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id,name,slug) VALUES ($1,$2,$3)`,
		orgID, "Upd Org", "upd-org-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,name,password_hash) VALUES ($1,$2,$3,$4)`,
		ownerID, "upd-"+uuid.NewString()+"@test.invalid", "Upd Owner", "x"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,slug) VALUES ($1,$2,$3,$4)`,
		projectID, orgID, "Upd Project", "upd-proj-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agents (id,org_id,owner_id,name,slug) VALUES ($1,$2,$3,$4,$5)`,
		agentID, orgID, ownerID, "Upd Agent", "upd-agent-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, agent_id, chunk_type, query_key, title, content, source_lines, gotchas, related)
		VALUES ($1, NULL, 'AGENT', $2, 'MEMORY', $3, $4, '{"body":"before"}'::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, chunkID, agentID, queryKey, "Upd chunk"); err != nil {
		t.Fatalf("insert agent chunk: %v", err)
	}

	tools := &Tools{pool: pool, embed: nil}
	title := "updated title"

	// The reproduction: a project_id is supplied AND the chunk is AGENT-scoped.
	_, err = tools.UpdateContext(ctx, projectID, UpdateContextInput{ID: chunkID, ChangeNote: "x", Title: &title})
	if err != nil {
		t.Fatalf("#139: update_context rejected an id read_context returns: %v", err)
	}

	// And the update must actually have applied.
	var got string
	if qerr := pool.QueryRow(ctx, `SELECT title FROM context_chunks WHERE id=$1`, chunkID).Scan(&got); qerr != nil {
		t.Fatalf("read back: %v", qerr)
	}
	if got != title {
		t.Fatalf("title = %q, want %q — the update reported success but changed nothing", got, title)
	}
}

// TestUpdateContext_OutOfScopePROJECTChunkIsHonest pins the #139 error-honesty
// requirement: updating a PROJECT-scoped chunk from a DIFFERENT project must
// say "out of scope", not "not found" — the two are different answers and the
// old message conflated them.
func TestUpdateContext_OutOfScopePROJECTChunkIsHonest(t *testing.T) {
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
	projA := uuid.NewString()
	projB := uuid.NewString()
	chunkID := uuid.NewString()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM context_chunks WHERE id=$1`, chunkID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id=ANY($1)`, []string{projA, projB})
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id=$1`, orgID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id,name,slug) VALUES ($1,$2,$3)`,
		orgID, "Honest Org", "honest-org-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("org: %v", err)
	}
	for _, p := range []string{projA, projB} {
		if _, err := pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,slug) VALUES ($1,$2,$3,$4)`,
			p, orgID, "P", "honest-proj-"+strings.ToLower(uuid.NewString())); err != nil {
			t.Fatalf("project: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, chunk_type, query_key, title, content, source_lines, gotchas, related)
		VALUES ($1, $2, 'PROJECT', 'SPEC', $3, $4, '{}'::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, chunkID, projA, "honest-"+strings.ToLower(uuid.NewString()), "Chunk in A"); err != nil {
		t.Fatalf("chunk: %v", err)
	}

	tools := &Tools{pool: pool, embed: nil}
	title := "x"
	_, err = tools.UpdateContext(ctx, projB, UpdateContextInput{ID: chunkID, ChangeNote: "x", Title: &title})
	if err == nil {
		t.Fatal("updating a chunk from another project returned nil; it must be refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "not found") {
		t.Fatalf("error = %q; an existing chunk in another project is OUT OF SCOPE, not 'not found' (#139)", msg)
	}
	if !strings.Contains(msg, "another project") && !strings.Contains(msg, "not in project") {
		t.Fatalf("error = %q; want a scope explanation, not an ambiguous message", msg)
	}

	// A genuinely absent id must still say not found.
	_, err = tools.UpdateContext(ctx, "", UpdateContextInput{ID: uuid.NewString(), ChangeNote: "x", Title: &title})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("absent id error = %v; want 'chunk not found'", err)
	}
}
