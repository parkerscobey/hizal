package mcp

import (
	"fmt"
	"testing"
	"time"

	"github.com/XferOps/winnow/internal/models"
	"github.com/pgvector/pgvector-go"
)

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
