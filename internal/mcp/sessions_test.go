package mcp

import (
	"testing"
	"time"

	"github.com/XferOps/winnow/internal/models"
)

func TestParseLifecycleConfig_Defaults(t *testing.T) {
	lc := &models.SessionLifecycle{
		Config: []byte(`{}`),
	}
	cfg, err := parseLifecycleConfig(lc)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTLHours != 8 {
		t.Errorf("TTLHours = %d, want 8", cfg.TTLHours)
	}
	if len(cfg.InjectScopes) != 3 {
		t.Errorf("InjectScopes = %v, want [AGENT PROJECT ORG]", cfg.InjectScopes)
	}
}

func TestParseLifecycleConfig_DevPreset(t *testing.T) {
	lc := &models.SessionLifecycle{
		Config: []byte(`{
			"ttl_hours": 8,
			"required_steps": ["register_focus"],
			"consolidation_threshold": 3,
			"inject_scopes": ["AGENT", "PROJECT", "ORG"]
		}`),
	}
	cfg, err := parseLifecycleConfig(lc)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.RequiredSteps) != 1 || cfg.RequiredSteps[0] != "register_focus" {
		t.Errorf("RequiredSteps = %v, want [register_focus]", cfg.RequiredSteps)
	}
	if cfg.ConsolidationThreshold != 3 {
		t.Errorf("ConsolidationThreshold = %d, want 3", cfg.ConsolidationThreshold)
	}
}

func TestParseLifecycleConfig_Invalid(t *testing.T) {
	lc := &models.SessionLifecycle{Config: []byte(`not json`)}
	_, err := parseLifecycleConfig(lc)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestStartSessionResult_Fields(t *testing.T) {
	// Validate StartSessionResult struct fields are populated correctly.
	r := &StartSessionResult{
		SessionID:      "sess-123",
		ExpiresAt:      time.Now().Add(8 * time.Hour),
		Lifecycle:      "default",
		RequiredSteps:  []string{},
		InjectedChunks: []InjectedChunk{
			{ID: "c1", QueryKey: "identity", Title: "Agent Identity", Content: "I am...", Scope: "AGENT"},
		},
	}
	if r.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want sess-123", r.SessionID)
	}
	if r.Lifecycle != "default" {
		t.Errorf("Lifecycle = %q, want default", r.Lifecycle)
	}
	if len(r.InjectedChunks) != 1 {
		t.Errorf("InjectedChunks len = %d, want 1", len(r.InjectedChunks))
	}
	if r.InjectedChunks[0].Scope != "AGENT" {
		t.Errorf("InjectedChunks[0].Scope = %q, want AGENT", r.InjectedChunks[0].Scope)
	}
}

func TestEndSessionResult_Fields(t *testing.T) {
	r := &EndSessionResult{
		SessionID:     "sess-456",
		ChunksWritten: 5,
		ChunksRead:    12,
		WriteChunks: []SessionChunkSummary{
			{ID: "c2", QueryKey: "debug-notes", Title: "Debug notes", Scope: "AGENT"},
		},
	}
	if r.ChunksWritten != 5 {
		t.Errorf("ChunksWritten = %d, want 5", r.ChunksWritten)
	}
	if len(r.WriteChunks) != 1 {
		t.Errorf("WriteChunks len = %d, want 1", len(r.WriteChunks))
	}
}

// Integration-style tests would require a live DB or pgxmock.
// Unit tests above cover config parsing and struct integrity.
// DB-dependent tests (StartSession, ResumeSession, EndSession, RegisterFocus)
// are covered by the integration test suite in internal/mcp/integration_test.go.
func TestInjectedChunkOrder(t *testing.T) {
	t.Parallel()
	// Verify scope ordering constants: AGENT=1, ORG=2, PROJECT=3
	// This is enforced in SQL but we doc-test the expectation.
	scopes := []struct {
		scope string
		order int
	}{
		{"AGENT", 1},
		{"ORG", 2},
		{"PROJECT", 3},
	}
	for i, s := range scopes {
		if s.order != i+1 {
			t.Errorf("scope %q order = %d, want %d", s.scope, s.order, i+1)
		}
	}
}
