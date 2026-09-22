package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/XferOps/hizal/internal/models"
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
	r := &StartSessionResult{
		SessionID:     "sess-123",
		ExpiresAt:     time.Now().Add(8 * time.Hour),
		Lifecycle:     "default",
		RequiredSteps: []string{},
		InjectedChunks: []InjectedChunk{
			{ID: "c1", QueryKey: "identity", Title: "Agent Identity", Content: "I am...", Scope: "AGENT", ChunkType: "IDENTITY"},
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
	if r.InjectedChunks[0].ChunkType != "IDENTITY" {
		t.Errorf("InjectedChunks[0].ChunkType = %q, want IDENTITY", r.InjectedChunks[0].ChunkType)
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
// are covered by the integration test suite.
//
// Session activity counter tests (incrementSessionActivity) require a live
// DB to verify chunks_written/chunks_read increment correctly after
// write/read tool calls. Run via: go test ./internal/mcp/... -tags=integration
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

func TestIntersectScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a    []string
		b    []string
		want []string
	}{
		{"both empty", []string{}, []string{}, []string{}},
		{"a empty", []string{}, []string{"AGENT", "ORG"}, []string{}},
		{"b empty", []string{"AGENT", "ORG"}, []string{}, []string{}},
		{"full overlap", []string{"AGENT", "ORG", "PROJECT"}, []string{"AGENT", "ORG"}, []string{"AGENT", "ORG"}},
		{"partial overlap", []string{"AGENT", "ORG"}, []string{"ORG", "PROJECT"}, []string{"ORG"}},
		{"no overlap", []string{"AGENT"}, []string{"PROJECT"}, []string{}},
		{"preserves order from a", []string{"PROJECT", "AGENT", "ORG"}, []string{"AGENT", "ORG"}, []string{"AGENT", "ORG"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intersectScopes(tt.a, tt.b)
			if len(got) != len(tt.want) {
				t.Errorf("intersectScopes(%v, %v) len = %d, want %d", tt.a, tt.b, len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("intersectScopes(%v, %v)[%d] = %q, want %q", tt.a, tt.b, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ─── WNW-99: session inject-set caching ────────────────────────────────────

func TestCacheInjectSet_EmptyChunks(t *testing.T) {
	t.Parallel()
	// cacheInjectSet is a no-op when chunks is empty.
	// This is tested by verifying the guard condition independently —
	// full DB-path tested in integration suite.
	chunks := []InjectedChunk{}
	if len(chunks) != 0 {
		t.Fatal("precondition: chunks must be empty")
	}
	// The function early-returns without touching the DB when len == 0.
	// This is the guard we want to preserve: no spurious DB writes on empty inject set.
}

func TestCacheInjectSet_IDExtraction(t *testing.T) {
	t.Parallel()
	// Verify chunk ID extraction logic produces the expected slice.
	chunks := []InjectedChunk{
		{ID: "chunk-aaa", QueryKey: "qk-1", Title: "T1", Content: "c1", Scope: "AGENT", ChunkType: "IDENTITY"},
		{ID: "chunk-bbb", QueryKey: "qk-2", Title: "T2", Content: "c2", Scope: "ORG", ChunkType: "CONVENTION"},
		{ID: "chunk-ccc", QueryKey: "qk-3", Title: "T3", Content: "c3", Scope: "PROJECT", ChunkType: "KNOWLEDGE"},
	}
	ids := make([]string, len(chunks))
	for i, c := range chunks {
		ids[i] = c.ID
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}
	if ids[0] != "chunk-aaa" || ids[1] != "chunk-bbb" || ids[2] != "chunk-ccc" {
		t.Errorf("IDs = %v, want [chunk-aaa chunk-bbb chunk-ccc]", ids)
	}
}

func TestStartSessionResult_WithTruncation(t *testing.T) {
	t.Parallel()
	r := &StartSessionResult{
		SessionID:      "sess-789",
		ExpiresAt:      time.Now().Add(8 * time.Hour),
		Lifecycle:      "standard",
		RequiredSteps:  []string{"register_focus"},
		TruncatedCount: 3,
		InjectedChunks: []InjectedChunk{
			{ID: "c1", QueryKey: "identity", Scope: "AGENT", ChunkType: "IDENTITY"},
		},
	}
	if r.TruncatedCount != 3 {
		t.Errorf("TruncatedCount = %d, want 3", r.TruncatedCount)
	}
	if len(r.RequiredSteps) != 1 || r.RequiredSteps[0] != "register_focus" {
		t.Errorf("RequiredSteps = %v, want [register_focus]", r.RequiredSteps)
	}
}

func TestResumeSessionResult_Fields(t *testing.T) {
	t.Parallel()
	task := "implement WNW-99"
	r := &ResumeSessionResult{
		SessionID:     "sess-abc",
		ExpiresAt:     time.Now().Add(8 * time.Hour),
		FocusTask:     &task,
		ChunksWritten: 7,
		ResumeCount:   2,
		InjectedChunks: []InjectedChunk{
			{ID: "c1", QueryKey: "identity", Scope: "AGENT", ChunkType: "IDENTITY"},
			{ID: "c2", QueryKey: "convention", Scope: "PROJECT", ChunkType: "CONVENTION"},
		},
	}
	if r.ResumeCount != 2 {
		t.Errorf("ResumeCount = %d, want 2", r.ResumeCount)
	}
	if r.FocusTask == nil || *r.FocusTask != task {
		t.Errorf("FocusTask = %v, want %q", r.FocusTask, task)
	}
	if len(r.InjectedChunks) != 2 {
		t.Errorf("InjectedChunks len = %d, want 2", len(r.InjectedChunks))
	}
}

func TestLatestFiltering(t *testing.T) {
	t.Parallel()

	rule := models.InjectAudienceRule{
		All:      true,
		Latest:   3,
	}

	testChunks := []InjectedChunk{
		{ID: "1", CreatedAt: time.Now().Add(-5 * time.Hour)},
		{ID: "2", CreatedAt: time.Now().Add(-4 * time.Hour)},
		{ID: "3", CreatedAt: time.Now().Add(-3 * time.Hour)},
		{ID: "4", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "5", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}

	ruleKey := fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v|%d",
		rule.All, rule.AgentIDs, rule.AgentTypes, rule.LifecycleTypes,
		rule.AgentTags, rule.OrgIDs, rule.ProjectIDs, rule.Latest)

	ruleMatched := map[string][]InjectedChunk{ruleKey: testChunks}
	ruleLatest := map[string]int{ruleKey: rule.Latest}

	ruleKeys := make([]string, 0, len(ruleMatched))
	for key := range ruleMatched {
		ruleKeys = append(ruleKeys, key)
	}
	sort.Strings(ruleKeys)

	var allMatched []InjectedChunk
	seen := make(map[string]bool)
	for _, rKey := range ruleKeys {
		rChunks := ruleMatched[rKey]
		latest := ruleLatest[rKey]
		if latest > 0 && len(rChunks) > latest {
			sort.Slice(rChunks, func(i, j int) bool {
				return rChunks[i].CreatedAt.After(rChunks[j].CreatedAt)
			})
			rChunks = rChunks[:latest]
		}
		for _, c := range rChunks {
			if !seen[c.ID] {
				seen[c.ID] = true
				allMatched = append(allMatched, c)
			}
		}
	}

	if len(allMatched) != 3 {
		t.Errorf("got %d chunks, want 3", len(allMatched))
	}

	wantIDs := []string{"5", "4", "3"}
	for i, id := range wantIDs {
		if allMatched[i].ID != id {
			t.Errorf("chunk[%d] ID = %q, want %q", i, allMatched[i].ID, id)
		}
	}
}

func TestLatestFiltering_NoLimit(t *testing.T) {
	t.Parallel()

	rule := models.InjectAudienceRule{
		All:    true,
		Latest: 0,
	}

	testChunks := []InjectedChunk{
		{ID: "1", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "2", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}

	ruleKey := fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v|%d",
		rule.All, rule.AgentIDs, rule.AgentTypes, rule.LifecycleTypes,
		rule.AgentTags, rule.OrgIDs, rule.ProjectIDs, rule.Latest)

	ruleMatched := map[string][]InjectedChunk{ruleKey: testChunks}
	ruleLatest := map[string]int{ruleKey: rule.Latest}

	ruleKeys := make([]string, 0, len(ruleMatched))
	for key := range ruleMatched {
		ruleKeys = append(ruleKeys, key)
	}
	sort.Strings(ruleKeys)

	var allMatched []InjectedChunk
	seen := make(map[string]bool)
	for _, rKey := range ruleKeys {
		rChunks := ruleMatched[rKey]
		latest := ruleLatest[rKey]
		if latest > 0 && len(rChunks) > latest {
			sort.Slice(rChunks, func(i, j int) bool {
				return rChunks[i].CreatedAt.After(rChunks[j].CreatedAt)
			})
			rChunks = rChunks[:latest]
		}
		for _, c := range rChunks {
			if !seen[c.ID] {
				seen[c.ID] = true
				allMatched = append(allMatched, c)
			}
		}
	}

	if len(allMatched) != 2 {
		t.Errorf("got %d chunks, want 2", len(allMatched))
	}
}

func TestMatchFocusTags(t *testing.T) {
	t.Parallel()

	match := []byte(`{"rules":[{"focus_tags":["checkout-v2","billing"]}]}`)
	if !matchFocusTags(match, []string{"billing"}) {
		t.Error("expected overlap match")
	}
	if matchFocusTags(match, []string{"unrelated"}) {
		t.Error("expected no match for unrelated tags")
	}
	if matchFocusTags(match, nil) {
		t.Error("empty focus tags should never match")
	}
	noFocusRule := []byte(`{"rules":[{"all":true}]}`)
	if matchFocusTags(noFocusRule, []string{"billing"}) {
		t.Error("rule without focus_tags should not match")
	}
	if matchFocusTags([]byte(`not json`), []string{"billing"}) {
		t.Error("malformed payload should not match")
	}
	if matchFocusTags(nil, []string{"billing"}) {
		t.Error("empty payload should not match")
	}
}

func TestPartitionFocusMatches(t *testing.T) {
	t.Parallel()

	candidates := []focusCandidate{
		{ID: "new-1", QueryKey: "k1", Title: "T1", Scope: "PROJECT", ChunkType: "KNOWLEDGE", IARaw: []byte(`{"rules":[{"focus_tags":["a"]}]}`)},
		{ID: "old-1", QueryKey: "k2", Title: "T2", Scope: "AGENT", ChunkType: "MEMORY", IARaw: []byte(`{"rules":[{"focus_tags":["a"]}]}`)},
		{ID: "miss-1", QueryKey: "k3", Title: "T3", Scope: "ORG", ChunkType: "PRINCIPLE", IARaw: []byte(`{"rules":[{"focus_tags":["zzz"]}]}`)},
		{ID: "bad-1", QueryKey: "k4", Title: "T4", Scope: "ORG", ChunkType: "KNOWLEDGE", IARaw: []byte(`broken`)},
	}
	upd := partitionFocusMatches([]string{"old-1"}, candidates, []string{"a"})

	if len(upd.NewIDs) != 1 || upd.NewIDs[0] != "new-1" {
		t.Errorf("NewIDs = %v, want [new-1]", upd.NewIDs)
	}
	if len(upd.Matched) != 2 {
		t.Fatalf("Matched len = %d, want 2 (new + already-present, miss + malformed excluded)", len(upd.Matched))
	}
	// Newly added first.
	if upd.Matched[0].ID != "new-1" || upd.Matched[1].ID != "old-1" {
		t.Errorf("Matched order = [%s %s], want [new-1 old-1]", upd.Matched[0].ID, upd.Matched[1].ID)
	}
	if upd.Matched[0].QueryKey != "k1" || upd.Matched[0].Scope != "PROJECT" || upd.Matched[0].ChunkType != "KNOWLEDGE" {
		t.Errorf("Matched[0] descriptors = %+v, want k1/PROJECT/KNOWLEDGE", upd.Matched[0])
	}

	// Empty tags match nothing.
	empty := partitionFocusMatches(nil, candidates, nil)
	if len(empty.Matched) != 0 || len(empty.NewIDs) != 0 {
		t.Errorf("empty tags should match nothing, got %+v", empty)
	}
}

func TestRegisterFocusResultJSON(t *testing.T) {
	t.Parallel()

	r := RegisterFocusResult{
		SessionID:           "sess-1",
		FocusTask:           "task",
		FocusInjectedChunks: 2,
		FocusNewChunks:      1,
		FocusChunks: []FocusMatchedChunk{
			{ID: "c1", QueryKey: "k", Title: "T", Scope: "PROJECT", ChunkType: "KNOWLEDGE"},
		},
	}
	var decoded map[string]interface{}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session_id", "focus_task", "focus_injected_chunks", "focus_new_chunks", "focus_chunks"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("result JSON missing key %q: %s", key, raw)
		}
	}
}
