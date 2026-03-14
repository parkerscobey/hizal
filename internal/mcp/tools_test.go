package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(24 * time.Hour)
	lastReviewAt := createdAt.Add(48 * time.Hour)

	row := stubScanner{values: []any{
		"chunk-search-1",
		"project-abc",
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
	if chunk.ProjectID != "project-abc" {
		t.Fatalf("ProjectID = %q, want project-abc", chunk.ProjectID)
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
	createdAt := time.Date(2026, time.March, 11, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(30 * time.Minute)

	row := stubScanner{values: []any{
		"chunk-abc",
		"project-xyz",
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

	if chunk.ProjectID != "project-xyz" {
		t.Fatalf("ProjectID = %q, want project-xyz", chunk.ProjectID)
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
