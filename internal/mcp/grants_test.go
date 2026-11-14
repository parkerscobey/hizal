package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cross-org project grants (#141) — the tests Peter holds at PR time.
//
// The revocation test is FIRST on purpose: it proves a grant is a *boundary*,
// not a *label* — revoke, then the foreign agent cannot read or write, and its
// session is not silently orphaned. A grant that only blocks new attaches is not
// a revocation.

// grantFixture creates two orgs and a project owned by org A. Returns the ids.
func grantFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (orgA, orgB, projectA string) {
	t.Helper()
	orgA, orgB, projectA = uuid.NewString(), uuid.NewString(), uuid.NewString()
	owner := uuid.NewString()
	sfx := strings.ToLower(uuid.NewString())
	for id, slug := range map[string]string{orgA: "a-" + sfx, orgB: "b-" + sfx} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO orgs (id,name,slug,tier,stripe_subscription_status,is_personal)
			 VALUES ($1,$2,$3,'free','none',false)`, id, "Org "+slug, slug); err != nil {
			t.Fatalf("insert org: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`,
		owner, "u-"+sfx+"@test.invalid", "Owner"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,slug) VALUES ($1,$2,$3,$4)`,
		projectA, orgA, "Shared Project", "proj-"+sfx); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectA)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id=ANY($1)`, []string{orgA, orgB})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, owner)
	})
	return orgA, orgB, projectA
}

// TestProjectGrant_RevocationFailsClosed is THE #141 test: with a grant, org B
// reaches org A's project; after revoke, B is refused — on the NEXT call.
func TestProjectGrant_RevocationFailsClosed(t *testing.T) {
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

	orgA, orgB, projectA := grantFixture(ctx, t, pool)

	// Before any grant: org B cannot reach the project.
	if role, _ := projectReachableByOrg(ctx, pool, orgB, projectA); role != GrantNone {
		t.Fatalf("B reached A's project with no grant: role=%q", role)
	}
	// The home org always can, at member level.
	if role, _ := projectReachableByOrg(ctx, pool, orgA, projectA); role != GrantMember {
		t.Fatalf("home org role = %q, want member", role)
	}

	// Grant org B.
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_org_grants (project_id, org_id, role) VALUES ($1,$2,'member')`,
		projectA, orgB); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if role, _ := projectReachableByOrg(ctx, pool, orgB, projectA); role != GrantMember {
		t.Fatalf("after grant, B role = %q, want member", role)
	}

	// REVOKE — the boundary test. After the DELETE, the very next reachability
	// check must fail closed (not merely block new attaches).
	if _, err := pool.Exec(ctx,
		`DELETE FROM project_org_grants WHERE project_id=$1 AND org_id=$2`, projectA, orgB); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if role, _ := projectReachableByOrg(ctx, pool, orgB, projectA); role != GrantNone {
		t.Fatalf("after REVOKE, B still reaches A's project (role=%q) — the grant is a label, not a boundary", role)
	}
}

// TestValidateStartProject_HonoursGrantAndRevocation pins that the SESSION path
// (not just the raw helper) honours the grant and fails closed on revoke — the
// "session is not silently orphaned" half of the acceptance.
func TestValidateStartProject_HonoursGrantAndRevocation(t *testing.T) {
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

	orgA, orgB, projectA := grantFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}
	pid := projectA

	// B has no grant yet → B-scoped start is refused.
	if err := tools.validateStartProject(ctx, orgB, "", &pid); err == nil {
		t.Fatal("B started on A's project with no grant — should be refused")
	}
	// A (home org) is fine.
	if err := tools.validateStartProject(ctx, orgA, "", &pid); err != nil {
		t.Fatalf("home org refused: %v", err)
	}
	// Grant B → B-scoped start is allowed.
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_org_grants (project_id, org_id, role) VALUES ($1,$2,'member')`,
		projectA, orgB); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := tools.validateStartProject(ctx, orgB, "", &pid); err != nil {
		t.Fatalf("B refused after grant: %v", err)
	}
	// Revoke → B-scoped start is refused AGAIN (fail-closed on the next call).
	if _, err := pool.Exec(ctx,
		`DELETE FROM project_org_grants WHERE project_id=$1 AND org_id=$2`, projectA, orgB); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := tools.validateStartProject(ctx, orgB, "", &pid); err == nil {
		t.Fatal("B still started after REVOKE — revocation did not fail closed")
	}
}

// TestScopeFilter_CrossOrgReadScopeInvariant pins the read-scope invariant of
// #141 at the pure-function level: for a caller in org B reaching project P
// (owned by org A) via a grant, the default filter yields P's PROJECT chunks and
// EXCLUDES org A's ORG chunks. ORG visibility keys on the CALLER's org, so a
// cross-org attached agent sees exactly what an in-org attached agent sees —
// nothing more. If PROJECT visibility ever keyed off home org instead of the
// project id, or ORG stopped keying on the caller, this test goes red.
func TestScopeFilter_CrossOrgReadScopeInvariant(t *testing.T) {
	const (
		projectP = "11111111-1111-1111-1111-111111111111" // owned by org A
		orgA     = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		orgB     = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)

	// Caller: B-org agent attached to P. No explicit scope → default OR-group.
	clause, args := scopeFilter("", projectP, "", orgB, nil)

	// The PROJECT branch must reference the project (P) — not the caller's org.
	if !strings.Contains(clause, "'PROJECT' AND cc.project_id") {
		t.Fatalf("PROJECT branch missing or wrong: %q", clause)
	}
	if !containsArg(args, projectP) {
		t.Fatalf("PROJECT branch does not bind the project id P; args=%v", args)
	}
	// The ORG branch must bind the CALLER's org (B), so A's org chunks are excluded.
	if !strings.Contains(clause, "'ORG' AND cc.org_id") {
		t.Fatalf("ORG branch missing: %q", clause)
	}
	if !containsArg(args, orgB) {
		t.Fatalf("ORG branch does not bind the caller's org B; args=%v", args)
	}
	if containsArg(args, orgA) {
		t.Fatalf("filter binds org A — a B-org caller could read A's org chunks; args=%v", args)
	}

	// Explicit ORG scope from a B caller must key on B, never A.
	clause, args = scopeFilter("ORG", projectP, "", orgB, nil)
	if !containsArg(args, orgB) || containsArg(args, orgA) {
		t.Fatalf("explicit ORG scope bound the wrong org: clause=%q args=%v", clause, args)
	}
}

func containsArg(args []interface{}, want string) bool {
	for _, a := range args {
		if s, ok := a.(string); ok && s == want {
			return true
		}
	}
	return false
}

// TestRecordCrossOrgWrite_Attributable pins #141 acceptance 2: a write by an
// agent whose home org differs from the project's home org leaves an
// attributable audit row naming the writer's org; an in-org write does not.
func TestRecordCrossOrgWrite_Attributable(t *testing.T) {
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

	orgA, orgB, projectA := grantFixture(ctx, t, pool)
	resourceID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE resource_id=$1`, resourceID)
	})

	// In-org write: writerOrg == home org → no audit row.
	if got := RecordCrossOrgWrite(ctx, pool, orgA, uuid.NewString(), projectA, resourceID); got != "" {
		t.Fatalf("in-org write recorded a foreign org %q; want none", got)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE resource_id=$1`, resourceID).Scan(&n)
	if n != 0 {
		t.Fatalf("in-org write left %d audit rows; want 0", n)
	}

	// Cross-org write: writerOrg (B) != home (A) → one attributable row.
	if got := RecordCrossOrgWrite(ctx, pool, orgB, uuid.NewString(), projectA, resourceID); got != orgB {
		t.Fatalf("cross-org write recorded %q, want %q", got, orgB)
	}
	var action, orgCol string
	if err := pool.QueryRow(ctx,
		`SELECT action, org_id::text FROM audit_log WHERE resource_id=$1`, resourceID).Scan(&action, &orgCol); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != "cross_org_chunk_write" {
		t.Fatalf("action = %q", action)
	}
	if orgCol != orgA {
		t.Fatalf("audit row org = %q, want the project's home org %q", orgCol, orgA)
	}
}
