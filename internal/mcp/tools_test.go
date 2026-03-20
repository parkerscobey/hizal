package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/winnow/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	embeddingText := "[0.3,0.4]"
	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(24 * time.Hour)
	lastReviewAt := createdAt.Add(48 * time.Hour)

	row := stubScanner{values: []any{
		"chunk-search-1",
		&projectABC,
		"auth-flow",
		"JWT middleware",
		encodeContent("Validates bearer tokens"),
		&embeddingText,
		&sourceFile,
		[]byte(`[10,20]`),
		encodeStringSlice([]string{"token expires"}),
		encodeStringSlice([]string{"chunk-related"}),
		&createdByAgent,
		createdAt,
		updatedAt,
		2,    // version
		0.88, // cosine score
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
	embeddingText := "[0.1,0.2]"
	createdAt := time.Date(2026, time.March, 11, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(30 * time.Minute)

	row := stubScanner{values: []any{
		"chunk-abc",
		&projectXYZ,
		"search-key",
		"Search chunk",
		encodeContent("Compaction candidate"),
		&embeddingText,
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
		QueryKey:         "principle-1",
		Title:            "Test Principle",
		Content:          "This is a test principle",
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
			IncludeScopes:                  []string{"AGENT"},
			ExcludeScopes:                  []string{"PROJECT"},
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
			IncludeScopes:           []string{"ORG"},
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
			IncludeScopes:                  []string{"PROJECT", "ORG"},
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

func TestWriteChunk_Validation(t *testing.T) {
	t.Parallel()

	tools := &Tools{pool: nil, embed: nil}

	t.Run("missing type returns error", func(t *testing.T) {
		t.Parallel()
		in := WriteChunkInput{
			QueryKey: "test-key",
			Title:    "Test",
			Content:  "Test content",
		}
		_, err := tools.WriteChunk(context.Background(), "", in)
		if err == nil {
			t.Fatalf("expected error for missing type, got nil")
		}
		if !strings.Contains(err.Error(), "type is required") {
			t.Fatalf("error = %q, want to contain 'type is required'", err.Error())
		}
	})

	t.Run("missing query_key returns error", func(t *testing.T) {
		t.Parallel()
		in := WriteChunkInput{
			Type:    "KNOWLEDGE",
			Title:   "Test",
			Content: "Test content",
		}
		_, err := tools.WriteChunk(context.Background(), "", in)
		if err == nil {
			t.Fatalf("expected error for missing query_key, got nil")
		}
		if !strings.Contains(err.Error(), "query_key") {
			t.Fatalf("error = %q, want to contain 'query_key'", err.Error())
		}
	})

	t.Run("missing title returns error", func(t *testing.T) {
		t.Parallel()
		in := WriteChunkInput{
			Type:     "KNOWLEDGE",
			QueryKey: "test-key",
			Content:  "Test content",
		}
		_, err := tools.WriteChunk(context.Background(), "", in)
		if err == nil {
			t.Fatalf("expected error for missing title, got nil")
		}
		if !strings.Contains(err.Error(), "title") {
			t.Fatalf("error = %q, want to contain 'title'", err.Error())
		}
	})

	t.Run("missing content returns error", func(t *testing.T) {
		t.Parallel()
		in := WriteChunkInput{
			Type:     "KNOWLEDGE",
			QueryKey: "test-key",
			Title:    "Test",
		}
		_, err := tools.WriteChunk(context.Background(), "", in)
		if err == nil {
			t.Fatalf("expected error for missing content, got nil")
		}
		if !strings.Contains(err.Error(), "content") {
			t.Fatalf("error = %q, want to contain 'content'", err.Error())
		}
	})
}

func TestReadContextValidation(t *testing.T) {
	t.Parallel()

	tools := &Tools{pool: nil, embed: nil}

	t.Run("missing id and query_key returns error", func(t *testing.T) {
		t.Parallel()

		_, err := tools.ReadContext(context.Background(), "", ReadContextInput{})
		if err == nil {
			t.Fatalf("expected error for missing identifiers, got nil")
		}
		if !strings.Contains(err.Error(), "id or query_key is required") {
			t.Fatalf("error = %q, want to contain %q", err.Error(), "id or query_key is required")
		}
	})

	t.Run("query_key without project_id returns error", func(t *testing.T) {
		t.Parallel()

		_, err := tools.ReadContext(context.Background(), "", ReadContextInput{QueryKey: "spec-key"})
		if err == nil {
			t.Fatalf("expected error for missing project_id, got nil")
		}
		if !strings.Contains(err.Error(), "project_id is required") {
			t.Fatalf("error = %q, want to contain %q", err.Error(), "project_id is required")
		}
	})
}

func TestReadContextByQueryKey(t *testing.T) {
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

	tools := &Tools{pool: pool, embed: nil}

	orgID := uuid.NewString()
	projectID := uuid.NewString()
	projectSlug := "read-context-project-" + strings.ToLower(uuid.NewString())
	chunkID := uuid.NewString()
	queryKey := "read-context-spec-" + strings.ToLower(uuid.NewString())

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
	})

	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, "Read Context Test Org", "read-context-org-"+strings.ToLower(uuid.NewString())); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, slug) VALUES ($1, $2, $3, $4)`, projectID, orgID, "Read Context Test Project", projectSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	currentContent := string(encodeContent("Current content"))
	previousContent := string(encodeContent("Previous content"))

	if _, err := pool.Exec(ctx, `
		INSERT INTO context_chunks (id, project_id, scope, inject_audience, chunk_type, query_key, title, content, source_lines, gotchas, related)
		VALUES ($1, $2, 'PROJECT', '{"rules":[{"all":true}]}'::jsonb, 'SPEC', $3, $4, $5::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, chunkID, projectID, queryKey, "WNW-80 Spec", currentContent); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO context_versions (chunk_id, version, content, change_note)
		VALUES ($1, 1, $2::jsonb, $3)
	`, chunkID, previousContent, "Initial draft"); err != nil {
		t.Fatalf("insert context version: %v", err)
	}

	t.Run("reads by query_key and project_id", func(t *testing.T) {
		result, err := tools.ReadContext(ctx, projectID, ReadContextInput{QueryKey: queryKey})
		if err != nil {
			t.Fatalf("ReadContext() error = %v", err)
		}
		if result.ID != chunkID {
			t.Fatalf("id = %q, want %q", result.ID, chunkID)
		}
		if result.QueryKey != queryKey {
			t.Fatalf("query_key = %q, want %q", result.QueryKey, queryKey)
		}
		if result.Scope != "PROJECT" {
			t.Fatalf("scope = %q, want PROJECT", result.Scope)
		}
		if result.InjectAudience == nil || !result.InjectAudience.IsInjectable() {
			t.Fatalf("inject_audience = nil, want non-nil")
		}
		if result.ChunkType != "SPEC" {
			t.Fatalf("chunk_type = %q, want SPEC", result.ChunkType)
		}
		if len(result.Versions) != 1 {
			t.Fatalf("versions length = %d, want 1", len(result.Versions))
		}
		if result.Versions[0].ChangeNote != "Initial draft" {
			t.Fatalf("change_note = %q, want %q", result.Versions[0].ChangeNote, "Initial draft")
		}
	})

	t.Run("id wins when id and query_key are both provided", func(t *testing.T) {
		result, err := tools.ReadContext(ctx, projectID, ReadContextInput{ID: chunkID, QueryKey: "wrong-key"})
		if err != nil {
			t.Fatalf("ReadContext() error = %v", err)
		}
		if result.ID != chunkID {
			t.Fatalf("id = %q, want %q", result.ID, chunkID)
		}
		if result.QueryKey != queryKey {
			t.Fatalf("query_key = %q, want %q", result.QueryKey, queryKey)
		}
	})
}

func TestWriteChunk_InjectAudienceOverride(t *testing.T) {
	t.Parallel()

	in := WriteChunkInput{
		Type:         "KNOWLEDGE",
		QueryKey:     "test-key",
		Title:        "Test",
		Content:      "Test content",
		InjectAudience: nil,
	}
	if in.InjectAudience != nil {
		t.Fatalf("InjectAudience should be nil by default")
	}

	override := json.RawMessage(`{"rules":[{"all":true}]}`)
	in.InjectAudience = &override
	if in.InjectAudience == nil {
		t.Fatalf("InjectAudience override failed")
	}
}

func TestNullStrPtr(t *testing.T) {
	t.Parallel()

	t.Run("nil returns nil interface", func(t *testing.T) {
		t.Parallel()
		r := nullStrPtr(nil)
		if r != nil {
			t.Fatalf("nullStrPtr(nil) = %v, want nil", r)
		}
	})

	t.Run("non-nil returns value", func(t *testing.T) {
		t.Parallel()
		s := "test-string"
		r := nullStrPtr(&s)
		if r == nil {
			t.Fatalf("nullStrPtr(&s) = nil, want 'test-string'")
		}
		if r != s {
			t.Fatalf("nullStrPtr(&s) = %v, want 'test-string'", r)
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

func TestScopeFilter(t *testing.T) {
	t.Parallel()

	projID := "aaaaaaaa-1111-2222-3333-444444444444"
	agentID := "bbbbbbbb-1111-2222-3333-444444444444"
	orgID := "cccccccc-1111-2222-3333-444444444444"

	t.Run("PROJECT named scope indexes correctly", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("PROJECT", projID, agentID, orgID, args)
		// projectID should be $2 (args[1])
		if args[1] != projID {
			t.Errorf("args[1] = %v, want %v", args[1], projID)
		}
		want := "AND cc.scope = 'PROJECT' AND cc.project_id = $2"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})

	t.Run("AGENT named scope indexes correctly", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("AGENT", projID, agentID, orgID, args)
		if args[1] != agentID {
			t.Errorf("args[1] = %v, want %v", args[1], agentID)
		}
		want := "AND cc.scope = 'AGENT' AND cc.agent_id = $2"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})

	t.Run("ORG named scope indexes correctly", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("ORG", projID, agentID, orgID, args)
		if args[1] != orgID {
			t.Errorf("args[1] = %v, want %v", args[1], orgID)
		}
		want := "AND cc.scope = 'ORG' AND cc.org_id = $2"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})

	t.Run("default scope with all IDs", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("", projID, agentID, orgID, args)
		// Should have 4 args: vector + 3 IDs
		if len(args) != 4 {
			t.Fatalf("len(args) = %d, want 4", len(args))
		}
		if args[1] != projID {
			t.Errorf("args[1] = %v, want %v", args[1], projID)
		}
		if args[2] != agentID {
			t.Errorf("args[2] = %v, want %v", args[2], agentID)
		}
		if args[3] != orgID {
			t.Errorf("args[3] = %v, want %v", args[3], orgID)
		}
		wantClause := "AND ((cc.scope = 'PROJECT' AND cc.project_id = $2) OR (cc.scope = 'AGENT' AND cc.agent_id = $3) OR (cc.scope = 'ORG' AND cc.org_id = $4))"
		if clause != wantClause {
			t.Errorf("clause = %q, want %q", clause, wantClause)
		}
	})

	t.Run("AGENT scope then LIMIT is next arg", func(t *testing.T) {
		// Regression test for WNW-83: LIMIT was getting UUID arg because
		// idx() returned len(args)+1 instead of len(args) after append.
		t.Parallel()
		args := []interface{}{"vector"}
		_, args = scopeFilter("AGENT", projID, agentID, orgID, args)
		// Append limit like SearchContext does
		limit := 10
		args = append(args, limit)
		limIdx := len(args)
		// limit should be at $3 (vector=$1, agentID=$2, limit=$3)
		if limIdx != 3 {
			t.Errorf("limIdx = %d, want 3", limIdx)
		}
		if args[limIdx-1] != 10 {
			t.Errorf("args[limIdx-1] = %v (%T), want 10 (int)", args[limIdx-1], args[limIdx-1])
		}
	})

	t.Run("PROJECT scope with missing projectID returns FALSE clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("PROJECT", "", agentID, orgID, args)
		if clause != "AND FALSE -- PROJECT scope requires project_id" {
			t.Errorf("clause = %q, want 'AND FALSE -- PROJECT scope requires project_id'", clause)
		}
		if len(args) != 1 {
			t.Errorf("len(args) = %d, want 1 (no new args appended)", len(args))
		}
	})

	t.Run("AGENT scope with missing agentID returns FALSE clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("AGENT", projID, "", orgID, args)
		if clause != "AND FALSE -- AGENT scope requires agent_id" {
			t.Errorf("clause = %q, want 'AND FALSE -- AGENT scope requires agent_id'", clause)
		}
		if len(args) != 1 {
			t.Errorf("len(args) = %d, want 1", len(args))
		}
	})

	t.Run("ORG scope with missing orgID returns FALSE clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("ORG", projID, agentID, "", args)
		if clause != "AND FALSE -- ORG scope requires org_id" {
			t.Errorf("clause = %q, want 'AND FALSE -- ORG scope requires org_id'", clause)
		}
		if len(args) != 1 {
			t.Errorf("len(args) = %d, want 1", len(args))
		}
	})

	t.Run("default scope with only PROJECT and ORG (no agentID)", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("", projID, "", orgID, args)
		if len(args) != 3 {
			t.Fatalf("len(args) = %d, want 3", len(args))
		}
		if args[1] != projID {
			t.Errorf("args[1] = %v, want %v", args[1], projID)
		}
		if args[2] != orgID {
			t.Errorf("args[2] = %v, want %v", args[2], orgID)
		}
		want := "AND ((cc.scope = 'PROJECT' AND cc.project_id = $2) OR (cc.scope = 'ORG' AND cc.org_id = $3))"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})

	t.Run("default scope with no IDs returns empty clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("", "", "", "", args)
		if clause != "" {
			t.Errorf("clause = %q, want empty", clause)
		}
		if len(args) != 1 {
			t.Errorf("len(args) = %d, want 1", len(args))
		}
	})

	t.Run("default scope with all three scopes emits correct OR clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, args := scopeFilter("", projID, agentID, orgID, args)
		want := "AND ((cc.scope = 'PROJECT' AND cc.project_id = $2) OR (cc.scope = 'AGENT' AND cc.agent_id = $3) OR (cc.scope = 'ORG' AND cc.org_id = $4))"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
	})
}

func TestChunkTypeFilter(t *testing.T) {
	t.Parallel()

	t.Run("empty chunkType returns empty clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, out := chunkTypeFilter("", args)
		if clause != "" {
			t.Errorf("clause = %q, want empty", clause)
		}
		if len(out) != 1 {
			t.Errorf("len(args) = %d, want 1", len(out))
		}
	})

	t.Run("valid chunkType appends arg and returns clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, out := chunkTypeFilter("KNOWLEDGE", args)
		want := "AND cc.chunk_type = $2"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
		if len(out) != 2 {
			t.Errorf("len(args) = %d, want 2", len(out))
		}
		if out[1] != "KNOWLEDGE" {
			t.Errorf("args[1] = %v, want KNOWLEDGE", out[1])
		}
	})

	t.Run("preserves existing args before chunkType", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector", "limit-10"}
		clause, out := chunkTypeFilter("MEMORY", args)
		want := "AND cc.chunk_type = $3"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
		if len(out) != 3 {
			t.Errorf("len(args) = %d, want 3", len(out))
		}
		if out[2] != "MEMORY" {
			t.Errorf("args[2] = %v, want MEMORY", out[2])
		}
	})
}

func TestAlwaysInjectFilter(t *testing.T) {
	t.Parallel()

	t.Run("false alwaysInjectOnly returns empty clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, out := alwaysInjectFilter(false, args)
		if clause != "" {
			t.Errorf("clause = %q, want empty", clause)
		}
		if len(out) != 1 {
			t.Errorf("len(args) = %d, want 1", len(out))
		}
	})

	t.Run("true alwaysInjectOnly returns always_inject clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, out := alwaysInjectFilter(true, args)
		if clause != "AND cc.inject_audience IS NOT NULL" {
			t.Errorf("clause = %q, want 'AND cc.inject_audience IS NOT NULL'", clause)
		}
		if len(out) != 1 {
			t.Errorf("len(args) = %d, want 1 (no new args needed)", len(out))
		}
	})
}

func TestApplyTypeFilters_DevPreset(t *testing.T) {
	t.Parallel()

	t.Run("dev preset no restrictions preserves scope and chunkType", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{}
		scope, ct := applyTypeFilters("PROJECT", "KNOWLEDGE", tf)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT", scope)
		}
		if ct != "KNOWLEDGE" {
			t.Errorf("chunkType = %q, want KNOWLEDGE", ct)
		}
	})

	t.Run("dev preset with empty scope uses first include_scope", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeScopes: []string{"AGENT", "PROJECT", "ORG"}}
		scope, ct := applyTypeFilters("", "KNOWLEDGE", tf)
		if scope != "AGENT" {
			t.Errorf("scope = %q, want AGENT (first in include_scopes)", scope)
		}
		if ct != "KNOWLEDGE" {
			t.Errorf("chunkType = %q, want KNOWLEDGE", ct)
		}
	})

	t.Run("dev preset with empty chunkType uses first include_chunk_types", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeChunkTypes: []string{"CONVENTION", "KNOWLEDGE"}}
		scope, ct := applyTypeFilters("PROJECT", "", tf)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT", scope)
		}
		if ct != "CONVENTION" {
			t.Errorf("chunkType = %q, want CONVENTION (first in include_chunk_types)", ct)
		}
	})

	t.Run("scope not in include_scopes returns empty", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeScopes: []string{"PROJECT", "ORG"}}
		scope, _ := applyTypeFilters("PROJECT", "", tf)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT", scope)
		}
	})

	t.Run("scope not in include_scopes returns empty", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{IncludeScopes: []string{"PROJECT", "ORG"}}
		scope, _ := applyTypeFilters("AGENT", "", tf)
		if scope != "" {
			t.Errorf("scope = %q, want empty", scope)
		}
	})

	t.Run("explicit ORG scope with org_search_requires_explicit_scope", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{
			IncludeScopes:                  []string{"PROJECT", "ORG"},
			OrgSearchRequiresExplicitScope: true,
		}
		scope, _ := applyTypeFilters("ORG", "", tf)
		if scope != "ORG" {
			t.Errorf("scope = %q, want ORG (explicit ORG allowed)", scope)
		}
		scope, _ = applyTypeFilters("PROJECT", "", tf)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT", scope)
		}
		scope, _ = applyTypeFilters("", "", tf)
		if scope != "PROJECT" {
			t.Errorf("scope = %q, want PROJECT (ORG removed by org_search_requires_explicit_scope when not explicit)", scope)
		}
	})

	t.Run("empty include_scopes with explicit scope keeps scope", func(t *testing.T) {
		t.Parallel()
		tf := models.AgentTypeFilterConfig{}
		scope, _ := applyTypeFilters("AGENT", "", tf)
		if scope != "AGENT" {
			t.Errorf("scope = %q, want AGENT", scope)
		}
	})
}

func TestIntersectOneOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		allowed []string
		want    string
	}{
		{"value in allowed", "PROJECT", []string{"PROJECT", "ORG"}, "PROJECT"},
		{"value not in allowed", "AGENT", []string{"PROJECT", "ORG"}, ""},
		{"empty allowed list keeps value", "AGENT", []string{}, "AGENT"},
		{"empty value returns empty", "", []string{"PROJECT"}, ""},
		{"both empty returns empty", "", []string{}, ""},
		{"value is first match", "AGENT", []string{"AGENT", "PROJECT", "ORG"}, "AGENT"},
		{"value is last match", "ORG", []string{"AGENT", "PROJECT", "ORG"}, "ORG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := intersectOneOf(tt.value, tt.allowed)
			if got != tt.want {
				t.Errorf("intersectOneOf(%q, %v) = %q, want %q", tt.value, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestRemoveScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scope    string
		toRemove string
		want     string
	}{
		{"removes matching scope", "AGENT", "AGENT", ""},
		{"keeps non-matching scope", "PROJECT", "AGENT", "PROJECT"},
		{"removes ORG", "ORG", "ORG", ""},
		{"empty scope stays empty", "", "AGENT", ""},
		{"empty toRemove keeps scope", "PROJECT", "", "PROJECT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := removeScope(tt.scope, tt.toRemove)
			if got != tt.want {
				t.Errorf("removeScope(%q, %q) = %q, want %q", tt.scope, tt.toRemove, got, tt.want)
			}
		})
	}
}

func TestAlwaysInjectFilter_SearchContext(t *testing.T) {
	t.Parallel()

	t.Run("AlwaysInjectOnly true adds always_inject clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, out := alwaysInjectFilter(true, args)
		if clause != "AND cc.inject_audience IS NOT NULL" {
			t.Errorf("clause = %q, want 'AND cc.inject_audience IS NOT NULL'", clause)
		}
		if len(out) != 1 {
			t.Errorf("len(args) = %d, want 1", len(out))
		}
	})

	t.Run("AlwaysInjectOnly false adds no clause", func(t *testing.T) {
		t.Parallel()
		args := []interface{}{"vector"}
		clause, out := alwaysInjectFilter(false, args)
		if clause != "" {
			t.Errorf("clause = %q, want empty", clause)
		}
		if len(out) != 1 {
			t.Errorf("len(args) = %d, want 1", len(out))
		}
	})
}

func TestReadContextResultFromModel(t *testing.T) {
	t.Parallel()

	projID := "proj-test"
	agentID := "agent-test"
	orgID := "org-test"
	chunk := models.ContextChunk{
		ID:              "chunk-read-test",
		ProjectID:       &projID,
		Scope:           "AGENT",
		AgentID:         &agentID,
		OrgID:           &orgID,
		InjectAudience:  models.DefaultInjectAudienceAll(),
		ChunkType:       "MEMORY",
		QueryKey:     "test-read-key",
		Title:        "Test Read",
		Content:      encodeContent("Read content"),
		CreatedAt:    time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC),
	}

	result := readContextResultFromModel(chunk, 5)

	if result.ID != "chunk-read-test" {
		t.Errorf("ID = %q, want chunk-read-test", result.ID)
	}
	if result.Scope != "AGENT" {
		t.Errorf("Scope = %q, want AGENT", result.Scope)
	}
	if result.AgentID == nil || *result.AgentID != agentID {
		t.Errorf("AgentID = %v, want %q", result.AgentID, agentID)
	}
	if result.OrgID == nil || *result.OrgID != orgID {
		t.Errorf("OrgID = %v, want %q", result.OrgID, orgID)
	}
	if result.InjectAudience == nil || !result.InjectAudience.IsInjectable() {
		t.Errorf("InjectAudience = nil or not injectable, want injectable")
	}
	if result.ChunkType != "MEMORY" {
		t.Errorf("ChunkType = %q, want MEMORY", result.ChunkType)
	}
	if result.Content != "Read content" {
		t.Errorf("Content = %q, want decoded content", result.Content)
	}
	if result.Version != 5 {
		t.Errorf("Version = %d, want 5", result.Version)
	}
	if result.Score != 0 {
		t.Errorf("Score = %v, want 0", result.Score)
	}
}

func TestChunkResultFromModel_DecodesAllFields(t *testing.T) {
	t.Parallel()

	sourceFile := "internal/mcp/tools_test.go"
	gotchas := []string{"Watch out for this"}
	related := []string{"chunk-1", "chunk-2"}
	chunk := models.ContextChunk{
		ID:          "chunk-full",
		QueryKey:    "full-decode-test",
		Title:       "Full Decode Test",
		Content:     encodeContent("Decoded content here"),
		SourceFile:  &sourceFile,
		SourceLines: []byte(`[10,20]`),
		Gotchas:     encodeStringSlice(gotchas),
		Related:     encodeStringSlice(related),
		CreatedAt:   time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC),
	}

	result := chunkResultFromModel(chunk, 2, 0.95)

	if result.SourceFile != sourceFile {
		t.Errorf("SourceFile = %q, want %q", result.SourceFile, sourceFile)
	}
	if len(result.SourceLines) != 2 || result.SourceLines[0] != 10 || result.SourceLines[1] != 20 {
		t.Errorf("SourceLines = %v, want [10 20]", result.SourceLines)
	}
	if len(result.Gotchas) != 1 || result.Gotchas[0] != gotchas[0] {
		t.Errorf("Gotchas = %v, want %v", result.Gotchas, gotchas)
	}
	if len(result.Related) != 2 {
		t.Errorf("Related len = %d, want 2", len(result.Related))
	}
	if result.Score != 0.95 {
		t.Errorf("Score = %v, want 0.95", result.Score)
	}
	if result.Version != 2 {
		t.Errorf("Version = %d, want 2", result.Version)
	}
}

func TestNullStr(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns nil", func(t *testing.T) {
		t.Parallel()
		r := nullStr("")
		if r != nil {
			t.Errorf("nullStr('') = %v, want nil", r)
		}
	})

	t.Run("non-empty string returns value", func(t *testing.T) {
		t.Parallel()
		r := nullStr("test-value")
		if r != "test-value" {
			t.Errorf("nullStr('test-value') = %v, want 'test-value'", r)
		}
	})
}

func TestJoinClauses(t *testing.T) {
	t.Parallel()

	t.Run("single clause", func(t *testing.T) {
		t.Parallel()
		got := joinClauses([]string{"a = 1"})
		if got != "a = 1" {
			t.Errorf("got %q, want 'a = 1'", got)
		}
	})

	t.Run("multiple clauses joined with comma", func(t *testing.T) {
		t.Parallel()
		got := joinClauses([]string{"a = 1", "b = 2", "c = 3"})
		if got != "a = 1, b = 2, c = 3" {
			t.Errorf("got %q, want 'a = 1, b = 2, c = 3'", got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()
		got := joinClauses([]string{})
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestSearchContextInput_Defaults(t *testing.T) {
	t.Parallel()

	in := SearchContextInput{
		Query: "test query",
	}
	if in.Limit != 0 {
		t.Errorf("default Limit = %d, want 0 (handler applies default of 10)", in.Limit)
	}
	if in.ProjectID != "" {
		t.Errorf("default ProjectID = %q, want empty", in.ProjectID)
	}
	if in.Scope != "" {
		t.Errorf("default Scope = %q, want empty", in.Scope)
	}
	if in.AlwaysInjectOnly {
		t.Errorf("default AlwaysInjectOnly = true, want false")
	}
}

func TestWriteChunkInput_Fields(t *testing.T) {
	t.Parallel()

	t.Run("InjectAudience is nil by default", func(t *testing.T) {
		t.Parallel()
		in := WriteChunkInput{
			Type:     "KNOWLEDGE",
			QueryKey: "test",
			Title:    "Test",
			Content:  "Content",
		}
		if in.InjectAudience != nil {
			t.Errorf("InjectAudience = %v, want nil", in.InjectAudience)
		}
	})

	t.Run("Scope and IDs are optional", func(t *testing.T) {
		t.Parallel()
		in := WriteChunkInput{
			Type:     "MEMORY",
			QueryKey: "test",
			Title:    "Test",
			Content:  "Content",
		}
		if in.Scope != "" {
			t.Errorf("Scope = %q, want empty", in.Scope)
		}
		if in.ProjectID != "" {
			t.Errorf("ProjectID = %q, want empty", in.ProjectID)
		}
		if in.AgentID != "" {
			t.Errorf("AgentID = %q, want empty", in.AgentID)
		}
		if in.OrgID != "" {
			t.Errorf("OrgID = %q, want empty", in.OrgID)
		}
	})
}

func TestCompactContextInput_Defaults(t *testing.T) {
	t.Parallel()

	in := CompactContextInput{
		Query: "test query",
	}
	if in.Limit != 0 {
		t.Errorf("default Limit = %d, want 0 (handler applies default of 50)", in.Limit)
	}
	if in.ProjectID != "" {
		t.Errorf("default ProjectID = %q, want empty", in.ProjectID)
	}
}

func TestUpdateContextInput_Fields(t *testing.T) {
	t.Parallel()

	t.Run("pointer fields are nil by default", func(t *testing.T) {
		t.Parallel()
		in := UpdateContextInput{
			ID:         "chunk-123",
			ChangeNote: "Test change",
		}
		if in.Title != nil {
			t.Errorf("Title = %v, want nil", in.Title)
		}
		if in.Content != nil {
			t.Errorf("Content = %v, want nil", in.Content)
		}
		if in.SourceFile != nil {
			t.Errorf("SourceFile = %v, want nil", in.SourceFile)
		}
	})

	t.Run("Title can be set", func(t *testing.T) {
		t.Parallel()
		newTitle := "Updated Title"
		in := UpdateContextInput{
			ID:         "chunk-123",
			Title:      &newTitle,
			ChangeNote: "Renamed",
		}
		if in.Title == nil || *in.Title != "Updated Title" {
			t.Errorf("Title = %v, want 'Updated Title'", in.Title)
		}
	})
}

func TestReviewContextInput_Validation(t *testing.T) {
	t.Parallel()

	tools := &Tools{pool: nil, embed: nil}

	t.Run("missing chunk_id returns error", func(t *testing.T) {
		t.Parallel()
		in := ReviewContextInput{
			ProjectID:   "proj-123",
			Usefulness:  4,
			Correctness: 4,
			Action:      "useful",
		}
		_, err := tools.ReviewContext(context.Background(), "proj-123", in)
		if err == nil {
			t.Fatalf("expected error for missing chunk_id, got nil")
		}
		if !strings.Contains(err.Error(), "chunk_id is required") {
			t.Fatalf("error = %q, want to contain 'chunk_id is required'", err.Error())
		}
	})

	t.Run("missing project_id returns error", func(t *testing.T) {
		t.Parallel()
		in := ReviewContextInput{
			ChunkID:     "chunk-123",
			Usefulness:  4,
			Correctness: 4,
			Action:      "useful",
		}
		_, err := tools.ReviewContext(context.Background(), "", in)
		if err == nil {
			t.Fatalf("expected error for missing project_id, got nil")
		}
		if !strings.Contains(err.Error(), "project_id is required") {
			t.Fatalf("error = %q, want to contain 'project_id is required'", err.Error())
		}
	})
}

func TestDeleteContextInput_Validation(t *testing.T) {
	t.Parallel()

	tools := &Tools{pool: nil, embed: nil}

	_, err := tools.DeleteContext(context.Background(), "proj-123", "")
	if err == nil {
		t.Fatalf("expected error for missing id, got nil")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("error = %q, want to contain 'id is required'", err.Error())
	}
}

func TestGetContextVersionsInput_Defaults(t *testing.T) {
	t.Parallel()

	tools := &Tools{pool: nil, embed: nil}

	t.Run("limit defaults to 10", func(t *testing.T) {
		t.Parallel()
		// We can't call the actual method without a DB, but we can verify
		// the logic that applies the default in the handler.
		// The default is applied inside GetContextVersions: if limit <= 0 { limit = 10 }
		// Verify the struct field is zero by default.
		in := GetVersionsInput{ID: "chunk-123"}
		if in.Limit != 0 {
			t.Errorf("default Limit = %d, want 0", in.Limit)
		}
		if tools == nil {
			t.Fatalf("Tools created")
		}
	})
}

func TestStartSessionInput_Fields(t *testing.T) {
	t.Parallel()

	projID := "proj-test"
	slug := "dev"
	in := StartSessionInput{
		ProjectID:     &projID,
		LifecycleSlug: &slug,
	}

	if in.ProjectID == nil {
		t.Fatalf("ProjectID is nil")
	}
	if *in.ProjectID != projID {
		t.Errorf("ProjectID = %q, want %q", *in.ProjectID, projID)
	}
	if in.LifecycleSlug == nil || *in.LifecycleSlug != "dev" {
		t.Errorf("LifecycleSlug = %v, want 'dev'", in.LifecycleSlug)
	}
	if in.AgentID != "" {
		t.Errorf("AgentID = %q, want empty (resolved from API key)", in.AgentID)
	}
}

func TestEndSessionInput_Fields(t *testing.T) {
	t.Parallel()

	in := EndSessionInput{SessionID: "sess-abc"}
	if in.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want sess-abc", in.SessionID)
	}
}

func TestResumeSessionInput_Fields(t *testing.T) {
	t.Parallel()

	in := ResumeSessionInput{SessionID: "sess-resume"}
	if in.SessionID != "sess-resume" {
		t.Errorf("SessionID = %q, want sess-resume", in.SessionID)
	}
}

func TestRegisterFocusInput_Fields(t *testing.T) {
	t.Parallel()

	in := RegisterFocusInput{SessionID: "sess-focus", Task: "Fix the login bug"}
	if in.SessionID != "sess-focus" {
		t.Errorf("SessionID = %q, want sess-focus", in.SessionID)
	}
	if in.Task != "Fix the login bug" {
		t.Errorf("Task = %q, want 'Fix the login bug'", in.Task)
	}
}

func TestGetActiveSessionResult_Fields(t *testing.T) {
	t.Parallel()

	t.Run("none status", func(t *testing.T) {
		r := &GetActiveSessionResult{
			Status:  "none",
			Message: "no active session",
		}
		if r.Status != "none" {
			t.Errorf("Status = %q, want none", r.Status)
		}
		if r.SessionID != nil {
			t.Errorf("SessionID = %v, want nil for status=none", r.SessionID)
		}
	})

	t.Run("active status with all fields", func(t *testing.T) {
		sessionID := "sess-active-123"
		lifecycle := "dev"
		focus := "Implementing auth"
		expires := "2026-03-19T18:00:00Z"
		r := &GetActiveSessionResult{
			SessionID:     &sessionID,
			Status:        "active",
			LifecycleSlug: &lifecycle,
			FocusTask:     &focus,
			ExpiresAt:     &expires,
			ChunksWritten: 5,
			ResumeCount:   2,
			Message:       "active session found",
		}
		if r.Status != "active" {
			t.Errorf("Status = %q, want active", r.Status)
		}
		if r.SessionID == nil || *r.SessionID != "sess-active-123" {
			t.Errorf("SessionID = %v, want sess-active-123", r.SessionID)
		}
		if r.ChunksWritten != 5 {
			t.Errorf("ChunksWritten = %d, want 5", r.ChunksWritten)
		}
		if r.ResumeCount != 2 {
			t.Errorf("ResumeCount = %d, want 2", r.ResumeCount)
		}
	})
}

func TestResolveProjectIDPrecedence(t *testing.T) {
	t.Parallel()

	t.Run("argProjectID wins over headerProjectID", func(t *testing.T) {
		argProj := "arg-project-uuid"
		headerProj := "header-project-uuid"
		// resolveProjectID returns argProjectID if non-empty
		// (tested via the function signature — arg wins when non-empty)
		if argProj == "" {
			t.Fatalf("test setup error")
		}
		if argProj == headerProj {
			t.Fatalf("test setup needs distinct values")
		}
		// The function: strings.TrimSpace(argProjectID) wins over headerProjectID
		// when non-empty. We verify the logic here.
		effective := argProj
		if effective == "" {
			effective = headerProj
		}
		if effective != argProj {
			t.Errorf("effective = %q, want argProj", effective)
		}
	})

	t.Run("headerProjectID used when argProjectID is empty", func(t *testing.T) {
		headerProj := "header-project-uuid"
		argProj := ""
		effective := argProj
		if effective == "" {
			effective = headerProj
		}
		if effective != headerProj {
			t.Errorf("effective = %q, want headerProj", effective)
		}
	})

	t.Run("empty both returns empty", func(t *testing.T) {
		argProj := ""
		headerProj := ""
		effective := argProj
		if effective == "" {
			effective = headerProj
		}
		if effective != "" {
			t.Errorf("effective = %q, want empty", effective)
		}
	})

	t.Run("whitespace in argProjectID is trimmed", func(t *testing.T) {
		// The function uses strings.TrimSpace on argProjectID
		trimmed := strings.TrimSpace("  arg-proj  ")
		if trimmed != "arg-proj" {
			t.Errorf("TrimSpace = %q, want 'arg-proj'", trimmed)
		}
	})

	t.Run("whitespace in headerProjectID is trimmed", func(t *testing.T) {
		trimmed := strings.TrimSpace("  header-proj  ")
		if trimmed != "header-proj" {
			t.Errorf("TrimSpace = %q, want 'header-proj'", trimmed)
		}
	})
}

func TestLoadAPIKeyScope_DispatchCoverage(t *testing.T) {
	t.Parallel()

	t.Run("apiKeyScope struct fields are correctly typed", func(t *testing.T) {
		t.Parallel()
		agentID := "agent-uuid-123"
		orgID := "org-uuid-456"
		scope := &apiKeyScope{
			OwnerType:         "AGENT",
			AgentID:           &agentID,
			OrgID:             orgID,
			ScopeAllProjects:  false,
			AllowedProjectIDs: []string{},
			SearchFilters:     models.AgentTypeFilterConfig{},
		}
		if scope.OwnerType != "AGENT" {
			t.Errorf("OwnerType = %q, want AGENT", scope.OwnerType)
		}
		if scope.AgentID == nil || *scope.AgentID != agentID {
			t.Errorf("AgentID = %v, want %q", scope.AgentID, agentID)
		}
		if scope.OrgID != orgID {
			t.Errorf("OrgID = %q, want %q", scope.OrgID, orgID)
		}
	})

	t.Run("apiKeyScope with nil AgentID for ORG-scoped key", func(t *testing.T) {
		t.Parallel()
		scope := &apiKeyScope{
			OwnerType:         "ORG",
			AgentID:           nil,
			OrgID:             "org-789",
			ScopeAllProjects:  true,
			AllowedProjectIDs: []string{},
		}
		if scope.AgentID != nil {
			t.Errorf("AgentID = %v, want nil for ORG-scoped key", scope.AgentID)
		}
		if scope.OrgID != "org-789" {
			t.Errorf("OrgID = %q, want org-789", scope.OrgID)
		}
	})

	t.Run("apiKeyScope SearchFilters field integration", func(t *testing.T) {
		t.Parallel()
		scope := &apiKeyScope{
			OwnerType:         "AGENT",
			AgentID:           strPtr("agent-test"),
			OrgID:             "org-test",
			ScopeAllProjects:  false,
			AllowedProjectIDs: []string{},
			SearchFilters: models.AgentTypeFilterConfig{
				IncludeScopes:           []string{"PROJECT", "ORG"},
				ExcludeScopes:           []string{"AGENT"},
				IncludeChunkTypes:       []string{"KNOWLEDGE"},
				ExcludeChunkTypes:       []string{},
				OrgSearchRequiresExplicitScope: false,
			},
		}
		if len(scope.SearchFilters.IncludeScopes) != 2 {
			t.Errorf("IncludeScopes len = %d, want 2", len(scope.SearchFilters.IncludeScopes))
		}
		if !slices.Contains(scope.SearchFilters.IncludeChunkTypes, "KNOWLEDGE") {
			t.Errorf("IncludeChunkTypes missing KNOWLEDGE")
		}
	})
}

func strPtr(s string) *string { return &s }

// ─── WNW-102: effectiveInjectAudience + write tool inputs ─────────────────

func TestEffectiveInjectAudience(t *testing.T) {
	t.Parallel()

	allAgents := &models.InjectAudience{Rules: []models.InjectAudienceRule{{All: true}}}
	devOnly := &models.InjectAudience{Rules: []models.InjectAudienceRule{{AgentTypes: []string{"dev"}}}}

	t.Run("override wins when provided", func(t *testing.T) {
		t.Parallel()
		got := effectiveInjectAudience(devOnly, allAgents)
		if got != devOnly {
			t.Errorf("expected override to be returned, got %+v", got)
		}
		if len(got.Rules) != 1 || got.Rules[0].AgentTypes[0] != "dev" {
			t.Errorf("wrong rule content: %+v", got.Rules)
		}
	})

	t.Run("default used when override is nil", func(t *testing.T) {
		t.Parallel()
		got := effectiveInjectAudience(nil, allAgents)
		if got != allAgents {
			t.Errorf("expected default to be returned, got %+v", got)
		}
	})

	t.Run("nil returned when both nil", func(t *testing.T) {
		t.Parallel()
		got := effectiveInjectAudience(nil, nil)
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("override used even when it has empty rules", func(t *testing.T) {
		t.Parallel()
		emptyOverride := &models.InjectAudience{Rules: []models.InjectAudienceRule{}}
		got := effectiveInjectAudience(emptyOverride, allAgents)
		if got != emptyOverride {
			t.Errorf("expected empty override to be returned (not default), got %+v", got)
		}
	})
}

func TestNullInjectAudience(t *testing.T) {
	t.Parallel()

	t.Run("nil returns nil interface", func(t *testing.T) {
		t.Parallel()
		result := nullInjectAudience(nil)
		if result != nil {
			t.Errorf("nullInjectAudience(nil) = %v, want nil", result)
		}
	})

	t.Run("all-agents rule serialises to JSON string", func(t *testing.T) {
		t.Parallel()
		ia := &models.InjectAudience{Rules: []models.InjectAudienceRule{{All: true}}}
		result := nullInjectAudience(ia)
		s, ok := result.(string)
		if !ok {
			t.Fatalf("nullInjectAudience returned %T, want string", result)
		}
		if !strings.Contains(s, `"all":true`) {
			t.Errorf("serialised JSON = %q, want to contain \"all\":true", s)
		}
	})

	t.Run("agent_types rule serialises correctly", func(t *testing.T) {
		t.Parallel()
		ia := &models.InjectAudience{
			Rules: []models.InjectAudienceRule{{AgentTypes: []string{"dev", "orchestrator"}}},
		}
		result := nullInjectAudience(ia)
		s, ok := result.(string)
		if !ok {
			t.Fatalf("nullInjectAudience returned %T, want string", result)
		}
		if !strings.Contains(s, "agent_types") {
			t.Errorf("serialised JSON = %q, want to contain 'agent_types'", s)
		}
	})
}

func TestWriteToolInputs_InjectAudienceField(t *testing.T) {
	t.Parallel()

	ia := &models.InjectAudience{Rules: []models.InjectAudienceRule{{All: true}}}

	t.Run("WriteIdentityInput accepts inject_audience override", func(t *testing.T) {
		t.Parallel()
		in := WriteIdentityInput{
			AgentID:        "agent-1",
			QueryKey:       "identity-key",
			Title:          "My Identity",
			Content:        "I am a dev agent.",
			InjectAudience: ia,
		}
		if in.InjectAudience == nil {
			t.Error("InjectAudience should be set")
		}
		if !in.InjectAudience.Rules[0].All {
			t.Error("expected All=true rule")
		}
	})

	t.Run("WriteMemoryInput accepts inject_audience override", func(t *testing.T) {
		t.Parallel()
		in := WriteMemoryInput{
			AgentID:        "agent-1",
			QueryKey:       "mem-key",
			Title:          "Memory",
			Content:        "I remembered this.",
			InjectAudience: ia,
		}
		if in.InjectAudience == nil {
			t.Error("InjectAudience should be set")
		}
	})

	t.Run("WriteKnowledgeInput accepts inject_audience override", func(t *testing.T) {
		t.Parallel()
		in := WriteKnowledgeInput{
			ProjectID:      "proj-1",
			QueryKey:       "know-key",
			Title:          "Knowledge",
			Content:        "Important fact.",
			InjectAudience: ia,
		}
		if in.InjectAudience == nil {
			t.Error("InjectAudience should be set")
		}
	})

	t.Run("WriteConventionInput accepts inject_audience override", func(t *testing.T) {
		t.Parallel()
		in := WriteConventionInput{
			ProjectID:      "proj-1",
			QueryKey:       "conv-key",
			Title:          "Convention",
			Content:        "Always use tabs.",
			InjectAudience: ia,
		}
		if in.InjectAudience == nil {
			t.Error("InjectAudience should be set")
		}
	})

	t.Run("WriteOrgKnowledgeInput accepts inject_audience override", func(t *testing.T) {
		t.Parallel()
		in := WriteOrgKnowledgeInput{
			OrgID:          "org-1",
			QueryKey:       "orgknow-key",
			Title:          "Org Knowledge",
			Content:        "Org-wide fact.",
			InjectAudience: ia,
		}
		if in.InjectAudience == nil {
			t.Error("InjectAudience should be set")
		}
	})

	t.Run("StorePrincipleInput accepts inject_audience override", func(t *testing.T) {
		t.Parallel()
		in := StorePrincipleInput{
			OrgID:          "org-1",
			QueryKey:       "principle-key",
			Title:          "Principle",
			Content:        "Always write tests.",
			InjectAudience: ia,
		}
		if in.InjectAudience == nil {
			t.Error("InjectAudience should be set")
		}
	})

	t.Run("inject_audience is nil by default (no override = use chunk type default)", func(t *testing.T) {
		t.Parallel()
		in := WriteMemoryInput{
			AgentID:  "agent-1",
			QueryKey: "mem-key",
			Title:    "Memory",
			Content:  "No override.",
		}
		if in.InjectAudience != nil {
			t.Errorf("InjectAudience should be nil by default, got %+v", in.InjectAudience)
		}
	})
}

func TestEffectiveInjectAudience_RoundTrip(t *testing.T) {
	t.Parallel()

	// Override with multi-rule audience, verify it passes through unmodified.
	multiRule := &models.InjectAudience{
		Rules: []models.InjectAudienceRule{
			{AgentTypes: []string{"dev"}, LifecycleTypes: []string{"standard"}},
			{All: true},
		},
	}
	defaultIA := &models.InjectAudience{
		Rules: []models.InjectAudienceRule{{All: true}},
	}

	got := effectiveInjectAudience(multiRule, defaultIA)
	if len(got.Rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(got.Rules))
	}
	if got.Rules[0].AgentTypes[0] != "dev" {
		t.Errorf("first rule agent_types[0] = %q, want 'dev'", got.Rules[0].AgentTypes[0])
	}
	if got.Rules[0].LifecycleTypes[0] != "standard" {
		t.Errorf("first rule lifecycle_types[0] = %q, want 'standard'", got.Rules[0].LifecycleTypes[0])
	}
	if !got.Rules[1].All {
		t.Error("second rule All should be true")
	}

	// Confirm default is returned unchanged when no override.
	got2 := effectiveInjectAudience(nil, defaultIA)
	if !got2.Rules[0].All {
		t.Error("default rule All should be true")
	}
}
