package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/XferOps/hizal/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// provisionFixture builds an org + owning user + a user-owned API key with the
// create-agents scope, and wires a router with the real middleware so the test
// exercises APIKeyAuth end to end (not just the handler).
type provisionFixture struct {
	pool     *pgxpool.Pool
	router   chi.Router
	orgID    string
	userID   string
	keyPlain string
}

func newProvisionFixture(t *testing.T, ctx context.Context, canCreate bool) *provisionFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	orgID := uuid.NewString()
	userID := uuid.NewString()
	email := "provision-" + strings.ToLower(uuid.NewString()) + "@example.com"
	slug := "provision-org-" + strings.ToLower(uuid.NewString())[:8]

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM api_keys WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM agents WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1,$2,$3)`, orgID, "Provision Test Org", slug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1,$2,$3)`, userID, email, "Provision Test User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1,$2,'owner')`, userID, orgID); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	plaintext, keyHash, err := auth.GenerateAPIKey("provision-test")
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (owner_type, user_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids, can_create_agents)
		VALUES ('USER', $1, $2, $3, 'provision-test', TRUE, '{}', $4)
	`, userID, orgID, keyHash, canCreate); err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	h := NewAgentProvisionHandlers(pool, nil)
	r := chi.NewRouter()
	r.Use(APIKeyAuth(pool))
	r.Post("/v1/agents", h.CreateAgent)
	r.Post("/v1/agents/{id}/keys", h.CreateAgentKey)

	return &provisionFixture{pool: pool, router: r, orgID: orgID, userID: userID, keyPlain: plaintext}
}

func (f *provisionFixture) post(t *testing.T, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+f.keyPlain)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// errCode reads the nested error.code from an error response body, matching the
// writeError shape: {"error":{"code":...,"message":...}}.
func errCode(out map[string]any) any {
	e, ok := out["error"].(map[string]any)
	if !ok {
		return nil
	}
	return e["code"]
}

// 1. Scope off => 403 SCOPE_REQUIRED. This is the default state of every key.
func TestProvisionAgentRequiresScope(t *testing.T) {
	ctx := context.Background()
	f := newProvisionFixture(t, ctx, false) // can_create_agents = FALSE

	rec, out := f.post(t, "/v1/agents", `{"name":"Blocked","slug":"blocked-`+uuid.NewString()[:8]+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without scope, got %d body=%s", rec.Code, rec.Body.String())
	}
	if errCode(out) != "SCOPE_REQUIRED" {
		t.Fatalf("want SCOPE_REQUIRED, got %v", errCode(out))
	}
	// And nothing was created.
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM agents WHERE org_id=$1`, f.orgID).Scan(&n); err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if n != 0 {
		t.Fatalf("agent was created despite missing scope: n=%d", n)
	}
}

//  2. Happy path: creates the agent in the key's org, owned by the key's user,
//     and a minted key for it works as an AGENT-owned key.
func TestProvisionAgentWithScope(t *testing.T) {
	ctx := context.Background()
	f := newProvisionFixture(t, ctx, true)

	slug := "provisioned-" + uuid.NewString()[:8]
	rec, out := f.post(t, "/v1/agents", `{"name":"Provisioned","slug":"`+slug+`","type":"CODER"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if out["org_id"] != f.orgID {
		t.Fatalf("agent created in wrong org: want %s got %v", f.orgID, out["org_id"])
	}
	if out["owner_id"] != f.userID {
		t.Fatalf("agent owner should be the key's user: want %s got %v", f.userID, out["owner_id"])
	}
	agentID, _ := out["id"].(string)
	if agentID == "" {
		t.Fatalf("no agent id in response")
	}

	// Mint a key for the new agent.
	rec2, out2 := f.post(t, "/v1/agents/"+agentID+"/keys", `{"name":"provisioned-key"}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("want 201 minting key, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	minted, _ := out2["key"].(string)
	if minted == "" {
		t.Fatalf("no plaintext key returned")
	}

	// The minted key must resolve as an AGENT-owned key bound to the new agent.
	var ownerType string
	var boundAgent *string
	if err := f.pool.QueryRow(ctx,
		`SELECT owner_type, agent_id FROM api_keys WHERE key_hash=$1`, auth.HashKey(minted),
	).Scan(&ownerType, &boundAgent); err != nil {
		t.Fatalf("minted key not found: %v", err)
	}
	if ownerType != "AGENT" || boundAgent == nil || *boundAgent != agentID {
		t.Fatalf("minted key wrong shape: owner_type=%s agent=%v want AGENT/%s", ownerType, boundAgent, agentID)
	}
}

//  3. An AGENT-owned key cannot create agents, even with the scope — no
//     self-replication. This is the rule that keeps the roster human-controlled.
func TestProvisionAgentDeniedForAgentOwnedKey(t *testing.T) {
	ctx := context.Background()
	f := newProvisionFixture(t, ctx, true)

	// Create a parent agent to own an agent key.
	parentID := uuid.NewString()
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO agents (id, org_id, owner_id, name, slug) VALUES ($1,$2,$3,'Parent','parent-'||$4)`,
		parentID, f.orgID, f.userID, uuid.NewString()[:8],
	); err != nil {
		t.Fatalf("insert parent agent: %v", err)
	}
	plaintext, keyHash, err := auth.GenerateAPIKey("parent")
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	// AGENT-owned key WITH the scope flag set — the flag must still not help.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO api_keys (owner_type, agent_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids, can_create_agents)
		VALUES ('AGENT', $1, $2, $3, 'parent-key', FALSE, '{}', TRUE)
	`, parentID, f.orgID, keyHash); err != nil {
		t.Fatalf("insert agent key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewBufferString(`{"name":"Sibling","slug":"sib-`+uuid.NewString()[:8]+`"}`))
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent-owned key must be denied, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if errCode(out) != "AGENT_KEY_CANNOT_PROVISION" {
		t.Fatalf("want AGENT_KEY_CANNOT_PROVISION, got %v", errCode(out))
	}

	// Prove nothing was created.
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM agents WHERE org_id=$1`, f.orgID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 { // only the parent
		t.Fatalf("agent-owned key created an agent: n=%d (want 1, the parent)", n)
	}
}

// 4. A key in org A cannot mint a key for an agent in org B (cross-org guard).
func TestProvisionKeyCannotCrossOrgs(t *testing.T) {
	ctx := context.Background()
	f := newProvisionFixture(t, ctx, true) // key in org A

	// Build a second org + agent with a different owner user.
	otherOrg, otherUser := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM agents WHERE org_id=$1`, otherOrg)
		_, _ = f.pool.Exec(ctx, `DELETE FROM orgs WHERE id=$1`, otherOrg)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, otherUser)
	})
	if _, err := f.pool.Exec(ctx, `INSERT INTO orgs (id,name,slug) VALUES ($1,'Other Org',$2)`, otherOrg, "other-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO users (id,email,name) VALUES ($1,$2,'Other User')`, otherUser, "other-"+uuid.NewString()[:8]+"@example.com"); err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	foreignAgent := uuid.NewString()
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO agents (id,org_id,owner_id,name,slug) VALUES ($1,$2,$3,'Foreign','foreign-'||$4)`,
		foreignAgent, otherOrg, otherUser, uuid.NewString()[:8],
	); err != nil {
		t.Fatalf("insert foreign agent: %v", err)
	}

	rec, out := f.post(t, "/v1/agents/"+foreignAgent+"/keys", `{"name":"stolen"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org key mint must 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	if errCode(out) != "AGENT_NOT_FOUND" {
		t.Fatalf("want AGENT_NOT_FOUND (no org disclosure), got %v", errCode(out))
	}
	// Prove no key was minted for the foreign agent.
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE agent_id=$1`, foreignAgent).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("cross-org key was minted: n=%d", n)
	}
}
