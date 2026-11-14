package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Multi-session (#142) — the tests Peter holds at PR time: the anti-guess rule
// applies to BOTH get_active_session and resume_session, and exactly-one stays
// backwards compatible.

// msFixture creates an org + a user + an agent, and returns (orgID, agentID).
func msFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (orgID, agentID string) {
	t.Helper()
	orgID, agentID = uuid.NewString(), uuid.NewString()
	owner := uuid.NewString()
	sfx := strings.ToLower(uuid.NewString())
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id,name,slug,tier,stripe_subscription_status,is_personal)
		 VALUES ($1,$2,$3,'free','none',false)`, orgID, "MS Org "+sfx, "ms-"+sfx); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`,
		owner, "ms-"+sfx+"@test.invalid", "MS Owner"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO agents (id,org_id,owner_id,name,slug) VALUES ($1,$2,$3,$4,$5)`,
		agentID, orgID, owner, "MS Agent", "ms-agent-"+sfx); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE agent_id=$1`, agentID)
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE id=$1`, agentID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, owner)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id=$1`, orgID)
	})
	return orgID, agentID
}

// TestGetActiveSession_DisambiguatesOnN pins the anti-guess rule: exactly one
// active session returns it; two or more returns status="ambiguous" with the
// list — never a silent pick.
func TestGetActiveSession_DisambiguatesOnN(t *testing.T) {
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

	orgID, agentID := msFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}

	// Zero active sessions → "none".
	if res, err := tools.GetActiveSession(ctx, agentID); err != nil || res.Status != "none" {
		t.Fatalf("no session: status=%v err=%v, want none", statusOf(res), err)
	}

	// One → returned (backwards compatible).
	s1, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start #1: %v", err)
	}
	res, err := tools.GetActiveSession(ctx, agentID)
	if err != nil {
		t.Fatalf("get (1 session): %v", err)
	}
	if res.Status != "active" || res.SessionID == nil || *res.SessionID != s1.SessionID {
		t.Fatalf("one session: status=%q id=%v, want active/%s", res.Status, res.SessionID, s1.SessionID)
	}

	// Two → ambiguous, with BOTH listed, and NO single session_id picked.
	if _, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{}); err != nil {
		t.Fatalf("start #2 (multi-session should allow it): %v", err)
	}
	res, err = tools.GetActiveSession(ctx, agentID)
	if err != nil {
		t.Fatalf("get (2 sessions): %v", err)
	}
	if res.Status != "ambiguous" {
		t.Fatalf("two sessions: status=%q, want ambiguous — it must not pick one", res.Status)
	}
	if res.SessionID != nil {
		t.Fatalf("ambiguous result still carries a single session_id (%v); it must not guess", *res.SessionID)
	}
	if len(res.OpenSessions) != 2 {
		t.Fatalf("ambiguous result listed %d sessions, want 2", len(res.OpenSessions))
	}
	seen := map[string]bool{}
	for _, o := range res.OpenSessions {
		seen[o.SessionID] = true
	}
	if !seen[s1.SessionID] {
		t.Fatalf("the first session %s is missing from the open list", s1.SessionID)
	}
}

// TestStartSession_AllowsConcurrent pins the enforcement removal: a second
// start_session for the same agent succeeds (the unique index is gone).
func TestStartSession_AllowsConcurrent(t *testing.T) {
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

	orgID, agentID := msFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}

	a, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start #1: %v", err)
	}
	b, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("#142: a second start_session failed (%v) — concurrent sessions must be allowed", err)
	}
	if a.SessionID == b.SessionID {
		t.Fatal("two start_session calls returned the same id")
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE agent_id=$1 AND status='active'`, agentID).Scan(&n)
	if n != 2 {
		t.Fatalf("active session count = %d, want 2", n)
	}
}

// TestResumeSession_RequiresID pins that resume_session never guesses: it
// requires a session_id (a missing one is an error, not "latest"), so with N>1
// the caller must disambiguate — the other half of the anti-guess rule.
func TestResumeSession_RequiresID(t *testing.T) {
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

	orgID, agentID := msFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}

	// Two active sessions open.
	if _, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{}); err != nil {
		t.Fatalf("start #1: %v", err)
	}
	s2, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start #2: %v", err)
	}

	// No session_id → error (never "latest").
	if _, err := tools.ResumeSession(ctx, orgID, ResumeSessionInput{}); err == nil {
		t.Fatal("resume_session with no session_id succeeded; it must require one, never guess")
	}
	// Explicit id → resumes THAT session.
	if _, err := tools.ResumeSession(ctx, orgID, ResumeSessionInput{SessionID: s2.SessionID}); err != nil {
		t.Fatalf("resume with explicit id failed: %v", err)
	}
}

func statusOf(r *GetActiveSessionResult) string {
	if r == nil {
		return "<nil>"
	}
	return r.Status
}
