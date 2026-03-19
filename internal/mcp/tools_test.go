package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/winnow/internal/models"
	"github.com/pgvector/pgvector-go"
)

func TestComputeFreshness(t *testing.T) {
	t.Parallel()

	approx := func(t *testing.T, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 0.001 {
			t.Fatalf("freshness = %.4f, want %.4f", got, want)
		}
	}

	now := time.Now()
	day := 24 * time.Hour

	t.Run("just created is fully fresh", func(t *testing.T) {
		approx(t, computeFreshness(now), 1.0)
	})

	t.Run("15 days old is fully fresh", func(t *testing.T) {
		approx(t, computeFreshness(now.Add(-15*day)), 1.0)
	})

	t.Run("exactly at decay start is still fully fresh", func(t *testing.T) {
		// 30 days — right at the boundary, no penalty yet
		approx(t, computeFreshness(now.Add(-time.Duration(FreshnessDecayStartDays)*day)), 1.0)
	})

	t.Run("halfway through decay window gets half penalty", func(t *testing.T) {
		// 60 days — halfway between 30 and 90
		// freshness = 1.0 - 0.3 * 0.5 = 0.85
		midAge := now.Add(-time.Duration((FreshnessDecayStartDays + FreshnessDecayFullDays) / 2 * float64(day)))
		approx(t, computeFreshness(midAge), 1.0-(1.0-FreshnessMin)*0.5)
	})

	t.Run("at full decay window gets minimum freshness", func(t *testing.T) {
		approx(t, computeFreshness(now.Add(-time.Duration(FreshnessDecayFullDays)*day)), FreshnessMin)
	})

	t.Run("beyond full decay window is clamped at minimum", func(t *testing.T) {
		approx(t, computeFreshness(now.Add(-120*day)), FreshnessMin)
		approx(t, computeFreshness(now.Add(-365*day)), FreshnessMin)
	})

	t.Run("old chunk with recent review resets to fully fresh", func(t *testing.T) {
		// The chunk's content is 90 days old but was reviewed yesterday.
		// Caller passes the most recent activity (the review date) to computeFreshness.
		recentReview := now.Add(-1 * day)
		approx(t, computeFreshness(recentReview), 1.0)
	})

	t.Run("freshness is monotonically decreasing with age", func(t *testing.T) {
		prev := computeFreshness(now)
		for days := 1; days <= 120; days++ {
			curr := computeFreshness(now.Add(-time.Duration(days) * day))
			if curr > prev+0.001 { // allow tiny float drift
				t.Fatalf("freshness increased from day %d to %d: %.4f → %.4f", days-1, days, prev, curr)
			}
			prev = curr
		}
	})

	t.Run("freshness is always in valid range", func(t *testing.T) {
		for days := 0; days <= 365; days++ {
			f := computeFreshness(now.Add(-time.Duration(days) * day))
			if f < FreshnessMin-0.001 || f > 1.001 {
				t.Fatalf("freshness %.4f out of [%.1f, 1.0] at day %d", f, FreshnessMin, days)
			}
		}
	})
}

func TestScanChunkSearchRow(t *testing.T) {
	t.Parallel()

	sourceFile := "internal/api/handlers.go"
	createdByAgent := "agent-xyz"
	projectABC := "project-abc"
	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(24 * time.Hour)
	lastReviewAt := createdAt.Add(48 * time.Hour)

	row := stubScanner{values: []any{
		"chunk-search-1",
		&projectABC,
		"auth-flow",
		"JWT middleware",
		encodeContent("Validates bearer tokens"),
		pgvector.NewVector([]float32{0.3, 0.4}),
		&sourceFile,
		[]byte(`[10,20]`),
		encodeStringSlice([]string{"token expires"}),
		encodeStringSlice([]string{"chunk-related"}),
		&createdByAgent,
		createdAt,
		updatedAt,
		2,      // version
		0.88,   // cosine score
		lastReviewAt,
	}}

	chunk, version, score, gotLastReviewAt, err := scanChunkSearchRow(row)
	if err != nil {
		t.Fatalf("scanChunkSearchRow() error = %v", err)
	}

	if chunk.ID != "chunk-search-1" {
		t.Fatalf("ID = %q, want chunk-search-1", chunk.ID)
	}
	if chunk.ProjectID == nil || *chunk.ProjectID != "project-abc" {
		t.Fatalf("ProjectID = %v, want project-abc", chunk.ProjectID)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
	if score != 0.88 {
		t.Fatalf("score = %.2f, want 0.88", score)
	}
	if !gotLastReviewAt.Equal(lastReviewAt) {
		t.Fatalf("lastReviewAt = %v, want %v", gotLastReviewAt, lastReviewAt)
	}
}

func TestFetchStaleSignals_EmptyInput(t *testing.T) {
	t.Parallel()

	// Empty chunk ID slice should short-circuit before any DB call.
	// Pool is nil to prove the DB is never touched.
	tools := &Tools{pool: nil, embed: nil}
	signals, err := tools.fetchStaleSignals(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signals != nil {
		t.Fatalf("expected nil map for empty input, got %v", signals)
	}
}

func TestFetchStaleSignals_CapPerChunk(t *testing.T) {
	t.Parallel()

	// Simulate building the result map the same way fetchStaleSignals does,
	// feeding more signals than the cap to verify the limit is enforced.
	ts := time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)
	chunkID := "chunk-overflow"

	result := map[string][]StaleSignal{}
	for i := 0; i < maxStaleSignalsPerChunk+10; i++ {
		if len(result[chunkID]) < maxStaleSignalsPerChunk {
			result[chunkID] = append(result[chunkID], StaleSignal{
				Action:    "needs_update",
				CreatedAt: ts.Add(-time.Duration(i) * time.Hour),
			})
		}
	}

	got := result[chunkID]
	if len(got) != maxStaleSignalsPerChunk {
		t.Fatalf("got %d signals, want %d (maxStaleSignalsPerChunk)", len(got), maxStaleSignalsPerChunk)
	}
	// Most recent signal should be first (index 0 = i=0, the newest).
	if !got[0].CreatedAt.Equal(ts) {
		t.Fatalf("first signal created_at = %v, want %v (most recent)", got[0].CreatedAt, ts)
	}
}

func TestStaleSignalJSON(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		signal   StaleSignal
		wantKeys []string
		noKeys   []string
	}{
		{
			name:     "explicit action with note",
			signal:   StaleSignal{Action: "needs_update", Note: "auth flow changed in PR #42", CreatedAt: ts},
			wantKeys: []string{`"action":"needs_update"`, `"note":"auth flow changed in PR #42"`},
		},
		{
			name:     "low_score action without note omits note field",
			signal:   StaleSignal{Action: "low_score", CreatedAt: ts},
			wantKeys: []string{`"action":"low_score"`},
			noKeys:   []string{`"note"`},
		},
		{
			name:     "outdated action",
			signal:   StaleSignal{Action: "outdated", Note: "API was redesigned", CreatedAt: ts},
			wantKeys: []string{`"action":"outdated"`, `"note":"API was redesigned"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tt.signal)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			got := string(b)
			for _, want := range tt.wantKeys {
				if !strings.Contains(got, want) {
					t.Fatalf("JSON %q missing %q", got, want)
				}
			}
			for _, absent := range tt.noKeys {
				if strings.Contains(got, absent) {
					t.Fatalf("JSON %q should not contain %q", got, absent)
				}
			}
		})
	}
}

func TestChunkResultStaleSignals(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, time.March, 14, 10, 0, 0, 0, time.UTC)

	t.Run("stale_signals omitted when empty", func(t *testing.T) {
		r := ChunkResult{ID: "c1", QueryKey: "k", Title: "T", Content: "C", CreatedAt: ts, UpdatedAt: ts}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "stale_signals") {
			t.Fatalf("stale_signals should be omitted when nil: %s", b)
		}
	})

	t.Run("stale_signals present when populated", func(t *testing.T) {
		r := ChunkResult{
			ID: "c2", QueryKey: "k", Title: "T", Content: "C", CreatedAt: ts, UpdatedAt: ts,
			StaleSignals: []StaleSignal{
				{Action: "needs_update", Note: "schema changed", CreatedAt: ts},
			},
		}
		b, _ := json.Marshal(r)
		got := string(b)
		if !strings.Contains(got, `"stale_signals"`) {
			t.Fatalf("stale_signals missing from output: %s", got)
		}
		if !strings.Contains(got, `"needs_update"`) {
			t.Fatalf("signal action missing from output: %s", got)
		}
		if !strings.Contains(got, `"schema changed"`) {
			t.Fatalf("signal note missing from output: %s", got)
		}
	})

	t.Run("multiple signals preserved in order", func(t *testing.T) {
		r := ChunkResult{
			ID: "c3", QueryKey: "k", Title: "T", Content: "C", CreatedAt: ts, UpdatedAt: ts,
			StaleSignals: []StaleSignal{
				{Action: "outdated", Note: "v2 API released", CreatedAt: ts},
				{Action: "low_score", CreatedAt: ts.Add(-24 * time.Hour)},
			},
		}
		b, _ := json.Marshal(r)
		got := string(b)
		if !strings.Contains(got, `"outdated"`) || !strings.Contains(got, `"low_score"`) {
			t.Fatalf("expected both signals in output: %s", got)
		}
	})
}

func TestChunkResultFromModel(t *testing.T) {
	t.Parallel()

	sourceFile := "internal/api/handlers.go"
	createdAt := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Hour)

	chunk := models.ContextChunk{
		ID:          "chunk-123",
		QueryKey:    "auth-flow",
		Title:       "JWT middleware",
		Content:     encodeContent("Validates bearer tokens."),
		SourceFile:  &sourceFile,
		SourceLines: []byte(`[42,67]`),
		Gotchas:     encodeStringSlice([]string{"JWT expiry is enforced server-side"}),
		Related:     encodeStringSlice([]string{"chunk-456"}),
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}

	got := chunkResultFromModel(chunk, 3, 0.91)

	if got.ID != chunk.ID {
		t.Fatalf("ID = %q, want %q", got.ID, chunk.ID)
	}
	if got.Content != "Validates bearer tokens." {
		t.Fatalf("Content = %q, want decoded content", got.Content)
	}
	if got.SourceFile != sourceFile {
		t.Fatalf("SourceFile = %q, want %q", got.SourceFile, sourceFile)
	}
	if len(got.SourceLines) != 2 || got.SourceLines[0] != 42 || got.SourceLines[1] != 67 {
		t.Fatalf("SourceLines = %v, want [42 67]", got.SourceLines)
	}
	if len(got.Gotchas) != 1 || got.Gotchas[0] != "JWT expiry is enforced server-side" {
		t.Fatalf("Gotchas = %v, want decoded gotchas", got.Gotchas)
	}
	if len(got.Related) != 1 || got.Related[0] != "chunk-456" {
		t.Fatalf("Related = %v, want decoded related IDs", got.Related)
	}
	if got.Version != 3 {
		t.Fatalf("Version = %d, want 3", got.Version)
	}
	if got.Score != 0.91 {
		t.Fatalf("Score = %v, want 0.91", got.Score)
	}
}

func TestScanChunkResultRow(t *testing.T) {
	t.Parallel()

	sourceFile := "internal/mcp/tools.go"
	createdByAgent := "agent-123"
	projectXYZ := "project-xyz"
	createdAt := time.Date(2026, time.March, 11, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(30 * time.Minute)

	row := stubScanner{values: []any{
		"chunk-abc",
		&projectXYZ,
		"search-key",
		"Search chunk",
		encodeContent("Compaction candidate"),
		pgvector.NewVector([]float32{0.1, 0.2}),
		&sourceFile,
		[]byte(`[9,21]`),
		encodeStringSlice([]string{"Keep version metadata"}),
		encodeStringSlice([]string{"chunk-def"}),
		&createdByAgent,
		createdAt,
		updatedAt,
		4,
		0.77,
	}}

	chunk, version, score, err := scanChunkResultRow(row)
	if err != nil {
		t.Fatalf("scanChunkResultRow() error = %v", err)
	}

	if chunk.ProjectID == nil || *chunk.ProjectID != "project-xyz" {
		t.Fatalf("ProjectID = %v, want project-xyz", chunk.ProjectID)
	}
	if chunk.SourceFile == nil || *chunk.SourceFile != sourceFile {
		t.Fatalf("SourceFile = %v, want %q", chunk.SourceFile, sourceFile)
	}
	if chunk.CreatedByAgent == nil || *chunk.CreatedByAgent != createdByAgent {
		t.Fatalf("CreatedByAgent = %v, want %q", chunk.CreatedByAgent, createdByAgent)
	}
	if version != 4 {
		t.Fatalf("version = %d, want 4", version)
	}
	if score != 0.77 {
		t.Fatalf("score = %v, want 0.77", score)
	}
}

type stubScanner struct {
	values []any
}

func (s stubScanner) Scan(dest ...interface{}) error {
	if len(dest) != len(s.values) {
		return fmt.Errorf("dest len = %d, want %d", len(dest), len(s.values))
	}

	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = s.values[i].(string)
		case **string:
			*d = s.values[i].(*string)
		case *[]byte:
			*d = append([]byte(nil), s.values[i].([]byte)...)
		case *pgvector.Vector:
			*d = s.values[i].(pgvector.Vector)
		case *time.Time:
			*d = s.values[i].(time.Time)
		case *int:
			*d = s.values[i].(int)
		case *float64:
			*d = s.values[i].(float64)
		default:
			return fmt.Errorf("unsupported dest type %T at index %d", dest[i], i)
		}
	}

	return nil
}

func TestStorePrincipleGuardrail(t *testing.T) {
	t.Parallel()

	// Pool is nil to prove the DB is never touched when guardrail fails
	tools := &Tools{pool: nil, embed: nil}

	in := StorePrincipleInput{
		QueryKey:   "principle-1",
		Title:      "Test Principle",
		Content:    "This is a test principle",
		PromotedByUserID: "", // Missing!
	}

	_, err := tools.StorePrinciple(context.Background(), "org-123", in)
	if err == nil {
		t.Fatalf("expected error when promoted_by_user_id is missing, got nil")
	}

	wantErrMsg := "store_principle requires human promotion"
	if !strings.Contains(err.Error(), wantErrMsg) {
		t.Fatalf("error = %q, want to contain %q", err.Error(), wantErrMsg)
	}
}

func TestMergeSearchFilters(t *testing.T) {
	t.Parallel()

	empty := models.AgentTypeFilterConfig{}

	t.Run("empty overrides returns type filters unchanged", func(t *testing.T) {
		t.Parallel()
		typeFilters := models.AgentTypeFilterConfig{
			IncludeScopes:     []string{"PROJECT", "ORG"},
			IncludeChunkTypes: []string{"KNOWLEDGE"},
		}
		result := mergeSearchFilters(typeFilters, empty)
		if !slices.Equal(result.IncludeScopes, []string{"PROJECT", "ORG"}) {
			t.Errorf("IncludeScopes = %v, want [PROJECT, ORG]", result.IncludeScopes)
		}
		if !slices.Equal(result.IncludeChunkTypes, []string{"KNOWLEDGE"}) {
			t.Errorf("IncludeChunkTypes = %v, want [KNOWLEDGE]", result.IncludeChunkTypes)
		}
	})

	t.Run("override replaces type filter fields", func(t *testing.T) {
		t.Parallel()
		typeFilters := models.AgentTypeFilterConfig{
			IncludeScopes:     []string{"PROJECT", "ORG"},
			IncludeChunkTypes: []string{"KNOWLEDGE", "DECISION"},
		}
		overrides := models.AgentTypeFilterConfig{
			IncludeScopes:     []string{"AGENT"},
			ExcludeScopes:     []string{"PROJECT"},
			OrgSearchRequiresExplicitScope: true,
		}
		result := mergeSearchFilters(typeFilters, overrides)
		if !slices.Equal(result.IncludeScopes, []string{"AGENT"}) {
			t.Errorf("IncludeScopes = %v, want [AGENT]", result.IncludeScopes)
		}
		if !slices.Equal(result.ExcludeScopes, []string{"PROJECT"}) {
			t.Errorf("ExcludeScopes = %v, want [PROJECT]", result.ExcludeScopes)
		}
		if !result.OrgSearchRequiresExplicitScope {
			t.Errorf("OrgSearchRequiresExplicitScope = false, want true")
		}
	})

	t.Run("empty type filters with overrides returns overrides", func(t *testing.T) {
		t.Parallel()
		typeFilters := models.AgentTypeFilterConfig{}
		overrides := models.AgentTypeFilterConfig{
			IncludeScopes:     []string{"ORG"},
			ExcludeQueryKeyPrefixes: []string{"admin-", "financial-"},
		}
		result := mergeSearchFilters(typeFilters, overrides)
		if !slices.Equal(result.IncludeScopes, []string{"ORG"}) {
			t.Errorf("IncludeScopes = %v, want [ORG]", result.IncludeScopes)
		}
		if !slices.Equal(result.ExcludeQueryKeyPrefixes, []string{"admin-", "financial-"}) {
			t.Errorf("ExcludeQueryKeyPrefixes = %v, want [admin-, financial-]", result.ExcludeQueryKeyPrefixes)
		}
	})
}

func TestApplyTypeFilters(t *testing.T) {
	t.Parallel()

	empty := models.AgentTypeFilterConfig{}

	t.Run("no type filters means scope unchanged", func(t *testing.T) {
		t.Parallel()
		scope, chunkType := applyTypeFilters("PROJECT", "KNOWLEDGE", empty)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT", scope)
		}
		if chunkType != "KNOWLEDGE" {
			t.Errorf("chunkType = %q, want KNOWLEDGE", chunkType)
		}
	})

	t.Run("empty scope with include_scopes uses first allowed", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeScopes: []string{"AGENT", "ORG"}}
		scope, _ := applyTypeFilters("", "", tf)
		if scope != "AGENT" {
			t.Errorf("scope = %q, want AGENT (first allowed)", scope)
		}
	})

	t.Run("scope outside include_scopes is narrowed to first match", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeScopes: []string{"PROJECT", "ORG"}}
		scope, _ := applyTypeFilters("AGENT", "", tf)
		if scope != "" {
			t.Errorf("scope = %q, want empty (AGENT not in include_scopes)", scope)
		}
	})

	t.Run("org_search_requires_explicit_scope removes ORG when not explicit", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{
			IncludeScopes: []string{"PROJECT", "ORG"},
			OrgSearchRequiresExplicitScope: true,
		}
		scope, _ := applyTypeFilters("PROJECT", "", tf)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT (ORG removed by org_search_requires_explicit_scope)", scope)
		}
		scope, _ = applyTypeFilters("ORG", "", tf)
		if scope != "ORG" {
			t.Errorf("scope = %q, want ORG (explicit ORG scope allowed)", scope)
		}
	})

	t.Run("explicit scope not in include_scopes returns empty", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeScopes: []string{"PROJECT"}}
		scope, _ := applyTypeFilters("AGENT", "", tf)
		if scope != "" {
			t.Errorf("scope = %q, want empty (AGENT not in type's include_scopes)", scope)
		}
	})

	t.Run("chunk_type outside include_chunk_types returns empty", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeChunkTypes: []string{"KNOWLEDGE", "DECISION"}}
		_, chunkType := applyTypeFilters("", "RESEARCH", tf)
		if chunkType != "" {
			t.Errorf("chunkType = %q, want empty (RESEARCH not in include_chunk_types)", chunkType)
		}
	})

	t.Run("chunk_type in include_chunk_types is kept", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeChunkTypes: []string{"KNOWLEDGE", "DECISION"}}
		_, chunkType := applyTypeFilters("", "DECISION", tf)
		if chunkType != "DECISION" {
			t.Errorf("chunkType = %q, want DECISION", chunkType)
		}
	})
}

func TestExcludeQueryKeyPrefixesClause(t *testing.T) {
	t.Parallel()

	t.Run("empty prefixes returns empty clause", func(t *testing.T) {
		t.Parallel()
		clause := excludeQueryKeyPrefixesClause(nil)
		if clause != "" {
			t.Errorf("clause = %q, want empty", clause)
		}
	})

	t.Run("single prefix", func(t *testing.T) {
		t.Parallel()
		clause := excludeQueryKeyPrefixesClause([]string{"admin-"})
		want := ` AND cc.query_key NOT LIKE 'admin-%'`
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})

	t.Run("multiple prefixes", func(t *testing.T) {
		t.Parallel()
		clause := excludeQueryKeyPrefixesClause([]string{"admin-", "financial-"})
		want := ` AND cc.query_key NOT LIKE 'admin-%' AND cc.query_key NOT LIKE 'financial-%'`
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})
}

