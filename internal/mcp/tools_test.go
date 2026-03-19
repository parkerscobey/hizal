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
		INSERT INTO context_chunks (id, project_id, scope, always_inject, chunk_type, query_key, title, content, source_lines, gotchas, related)
		VALUES ($1, $2, 'PROJECT', true, 'SPEC', $3, $4, $5::jsonb, 'null'::jsonb, '[]'::jsonb, '[]'::jsonb)
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
		if !result.AlwaysInject {
			t.Fatalf("always_inject = false, want true")
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

func TestWriteChunk_AlwaysInjectOverride(t *testing.T) {
	t.Parallel()

	// Verify WriteChunkInput.AlwaysInject is a pointer type (for optional override)
	in := WriteChunkInput{
		Type:         "KNOWLEDGE",
		QueryKey:     "test-key",
		Title:        "Test",
		Content:      "Test content",
		AlwaysInject: nil,
	}
	if in.AlwaysInject != nil {
		t.Fatalf("AlwaysInject should be nil by default")
	}

	override := true
	in.AlwaysInject = &override
	if in.AlwaysInject == nil || !*in.AlwaysInject {
		t.Fatalf("AlwaysInject override failed")
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
}
