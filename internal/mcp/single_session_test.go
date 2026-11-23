package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Single active session per agent (010_sessions, restored by
// 032_single_session_again after the #142 multi-session experiment).
//
// These tests replace the #142 suite (TestGetActiveSession_DisambiguatesOnN,
// TestStartSession_AllowsConcurrent, TestResumeSession_RequiresID). The rule
// they now pin is the one Parker asked for: one session, and a second start is
// refused with the existing session named so the caller can resume it.

// ssFixture creates an org + a user + an agent, and returns (orgID, agentID).
func ssFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (orgID, agentID string) {
	t.Helper()
	orgID, agentID = uuid.NewString(), uuid.NewString()
	owner := uuid.NewString()
	sfx := strings.ToLower(uuid.NewString())
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id,name,slug,tier,stripe_subscription_status,is_personal)
		 VALUES ($1,$2,$3,'free','none',false)`, orgID, "SS Org "+sfx, "ss-"+sfx); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`,
		owner, "ss-"+sfx+"@test.invalid", "SS Owner"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO agents (id,org_id,owner_id,name,slug) VALUES ($1,$2,$3,$4,$5)`,
		agentID, orgID, owner, "SS Agent", "ss-agent-"+sfx); err != nil {
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

// TestStartSession_SecondIsRefused pins the enforcement: with one active session
// already open, a second start_session fails with ActiveSessionError naming the
// session that exists, so the caller can resume it rather than reading an
// opaque unique-constraint message.
func TestStartSession_SecondIsRefused(t *testing.T) {
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

	orgID, agentID := ssFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}

	first, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start #1: %v", err)
	}

	_, err = tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err == nil {
		t.Fatal("a second start_session succeeded — one active session per agent is the rule")
	}
	var activeErr *ActiveSessionError
	if !errors.As(err, &activeErr) {
		t.Fatalf("second start error = %v (%T), want *ActiveSessionError carrying the existing session", err, err)
	}
	if activeErr.SessionID != first.SessionID {
		t.Fatalf("ActiveSessionError.SessionID = %q, want the existing session %q", activeErr.SessionID, first.SessionID)
	}
	if activeErr.ExpiresAt.IsZero() {
		t.Fatal("ActiveSessionError.ExpiresAt is zero — the caller needs it to decide resume vs start")
	}

	// And the DB still holds exactly one.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE agent_id=$1 AND status='active'`, agentID).Scan(&n); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if n != 1 {
		t.Fatalf("active session count = %d, want 1", n)
	}
}

// TestStartSession_AfterEndSucceeds pins the other half: ending the session
// frees the slot, so the next start_session succeeds. Without this, "one active
// session" could be satisfied by never starting again.
func TestStartSession_AfterEndSucceeds(t *testing.T) {
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

	orgID, agentID := ssFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}

	first, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start #1: %v", err)
	}
	if _, err := tools.EndSession(ctx, orgID, EndSessionInput{SessionID: first.SessionID}); err != nil {
		t.Fatalf("end #1: %v", err)
	}
	second, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start after end failed (%v) — ending must free the slot", err)
	}
	if second.SessionID == first.SessionID {
		t.Fatal("start after end returned the ended session's id")
	}
}

// TestGetActiveSession_SingleIsReturned pins the normal path and the recovery
// hint: none → status "none" telling the caller to start one; one → that session.
func TestGetActiveSession_SingleIsReturned(t *testing.T) {
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

	orgID, agentID := ssFixture(ctx, t, pool)
	tools := &Tools{pool: pool, embed: nil}

	res, err := tools.GetActiveSession(ctx, agentID)
	if err != nil {
		t.Fatalf("get (no session): %v", err)
	}
	if res.Status != "none" {
		t.Fatalf("no session: status=%q, want none", res.Status)
	}
	if res.SessionID != nil {
		t.Fatalf("no session still carries a session_id (%v)", *res.SessionID)
	}
	if !strings.Contains(res.Message, "start_session") {
		t.Fatalf("none message = %q, want it to name start_session as the next call", res.Message)
	}

	s1, err := tools.StartSession(ctx, orgID, agentID, StartSessionInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, err = tools.GetActiveSession(ctx, agentID)
	if err != nil {
		t.Fatalf("get (1 session): %v", err)
	}
	if res.Status != "active" || res.SessionID == nil || *res.SessionID != s1.SessionID {
		t.Fatalf("one session: status=%q id=%v, want active/%s", res.Status, res.SessionID, s1.SessionID)
	}
	if len(res.OpenSessions) != 0 {
		t.Fatalf("one session: open_sessions=%v, want empty", res.OpenSessions)
	}
}
