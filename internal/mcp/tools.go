package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/XferOps/winnow/internal/embeddings"
	"github.com/XferOps/winnow/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Freshness decay constants. A chunk is considered "fresh" for the first
// freshnessDecayStartDays days after its last activity (update or review).
// After that, a linear penalty is applied up to a maximum of 30% at
// freshnessDecayFullDays. These values are intentionally exported so they
// can be referenced in tests and documentation.
const (
	FreshnessDecayStartDays float64 = 30  // no penalty before this age (days)
	FreshnessDecayFullDays  float64 = 90  // full penalty applied at this age (days)
	FreshnessMin            float64 = 0.7 // floor multiplier (max 30% penalty)
)

// computeFreshness returns a score multiplier in [FreshnessMin, 1.0] based on
// how recently the chunk was active. lastActivity should be the most recent of
// updated_at and the latest review created_at for the chunk.
func computeFreshness(lastActivity time.Time) float64 {
	ageDays := time.Since(lastActivity).Hours() / 24.0
	if ageDays <= FreshnessDecayStartDays {
		return 1.0
	}
	window := FreshnessDecayFullDays - FreshnessDecayStartDays
	decay := (ageDays - FreshnessDecayStartDays) / window
	if decay >= 1.0 {
		return FreshnessMin
	}
	return 1.0 - (1.0-FreshnessMin)*decay
}

// Tools holds all MCP tool implementations.
type Tools struct {
	pool  *pgxpool.Pool
	embed *embeddings.Client
}

func NewTools(pool *pgxpool.Pool, embed *embeddings.Client) *Tools {
	return &Tools{pool: pool, embed: embed}
}

// ---- Input/Output types ----

type WriteContextInput struct {
	ProjectID   string   `json:"project_id,omitempty"`
	QueryKey    string   `json:"query_key"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	SourceFile  string   `json:"source_file,omitempty"`
	SourceLines [2]int   `json:"source_lines,omitempty"`
	Gotchas     []string `json:"gotchas,omitempty"`
	Related     []string `json:"related,omitempty"`
}

type WriteContextResult struct {
	ID        string    `json:"id"`
	QueryKey  string    `json:"query_key"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

type SearchContextInput struct {
	ProjectID string `json:"project_id,omitempty"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
	QueryKey  string `json:"query_key,omitempty"`
}

type ChunkResult struct {
	ID          string    `json:"id"`
	QueryKey    string    `json:"query_key"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	SourceFile  string    `json:"source_file,omitempty"`
	SourceLines []int     `json:"source_lines,omitempty"`
	Gotchas     []string  `json:"gotchas,omitempty"`
	Related     []string  `json:"related,omitempty"`
	Score       float64   `json:"score,omitempty"`
	Freshness   float64   `json:"freshness,omitempty"`
	Version     int       `json:"version,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SearchContextResult struct {
	Results []ChunkResult `json:"results"`
}

type ReadContextInput struct {
	ProjectID string `json:"project_id,omitempty"`
	ID        string `json:"id"`
}

type VersionResult struct {
	Version    int       `json:"version"`
	ChangeNote string    `json:"change_note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type ReadContextResult struct {
	ChunkResult
	Versions []VersionResult `json:"versions"`
}

type UpdateContextInput struct {
	ProjectID   string   `json:"project_id,omitempty"`
	ID          string   `json:"id"`
	Title       *string  `json:"title,omitempty"`
	Content     *string  `json:"content,omitempty"`
	SourceFile  *string  `json:"source_file,omitempty"`
	SourceLines []int    `json:"source_lines,omitempty"`
	Gotchas     []string `json:"gotchas,omitempty"`
	Related     []string `json:"related,omitempty"`
	ChangeNote  string   `json:"change_note"`
}

type UpdateContextResult struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GetVersionsInput struct {
	ProjectID string `json:"project_id,omitempty"`
	ID        string `json:"id"`
	Limit     int    `json:"limit,omitempty"`
}

type GetVersionsResult struct {
	Versions []VersionResult `json:"versions"`
}

type CompactContextInput struct {
	ProjectID string `json:"project_id,omitempty"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
}

type CompactContextResult struct {
	Chunks []ChunkResult `json:"chunks"`
	Total  int           `json:"total"`
}

type ReviewContextInput struct {
	ProjectID       string `json:"project_id,omitempty"`
	ChunkID         string `json:"chunk_id"`
	Task            string `json:"task,omitempty"`
	Usefulness      int    `json:"usefulness"`
	UsefulnessNote  string `json:"usefulness_note,omitempty"`
	Correctness     int    `json:"correctness"`
	CorrectnessNote string `json:"correctness_note,omitempty"`
	Action          string `json:"action"`
}

type ReviewContextResult struct {
	ID        string    `json:"id"`
	ChunkID   string    `json:"chunk_id"`
	CreatedAt time.Time `json:"created_at"`
}

type DeleteContextInput struct {
	ProjectID string `json:"project_id,omitempty"`
	ID        string `json:"id"`
}

type DeleteContextResult struct {
	Deleted bool   `json:"deleted"`
	ID      string `json:"id"`
}

// ---- Helpers ----

func encodeContent(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

func decodeContent(b []byte) string {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return string(b)
	}
	return s
}

func encodeStringSlice(ss []string) []byte {
	if ss == nil {
		ss = []string{}
	}
	b, _ := json.Marshal(ss)
	return b
}

func decodeStringSlice(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	var ss []string
	_ = json.Unmarshal(b, &ss)
	return ss
}

func encodeSourceLines(lines [2]int) []byte {
	if lines[0] == 0 && lines[1] == 0 {
		return []byte("null")
	}
	b, _ := json.Marshal(lines)
	return b
}

func decodeSourceLines(b []byte) []int {
	if len(b) == 0 || string(b) == "null" || string(b) == "{}" {
		return nil
	}
	var lines []int
	_ = json.Unmarshal(b, &lines)
	return lines
}

func getVersion(ctx context.Context, pool *pgxpool.Pool, chunkID string) (int, error) {
	var v int
	err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM context_versions WHERE chunk_id = $1`, chunkID).Scan(&v)
	return v, err
}

// ---- Tool Implementations ----

func (t *Tools) WriteContext(ctx context.Context, projectID string, in WriteContextInput) (*WriteContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required (set X-Project-ID header)")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	emb, err := t.embed.Embed(ctx, in.Content)
	if err != nil {
		log.Printf("embedding failed: %v", err)
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}

	contentJSON := encodeContent(in.Content)
	gotchasJSON := encodeStringSlice(in.Gotchas)
	relatedJSON := encodeStringSlice(in.Related)
	sourceLinesJSON := encodeSourceLines(in.SourceLines)
	vec := pgvector.NewVector(emb)

	var id string
	var createdAt time.Time
	err = pool(t).QueryRow(ctx, `
		INSERT INTO context_chunks (project_id, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at
	`, projectID, in.QueryKey, in.Title, contentJSON, vec, nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
		Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert chunk: %w", err)
	}

	// Insert initial version
	_, err = pool(t).Exec(ctx, `
		INSERT INTO context_versions (chunk_id, version, content, change_note)
		VALUES ($1, 1, $2, 'Initial')
	`, id, contentJSON)
	if err != nil {
		return nil, fmt.Errorf("insert initial version: %w", err)
	}

	return &WriteContextResult{
		ID:        id,
		QueryKey:  in.QueryKey,
		Title:     in.Title,
		CreatedAt: createdAt,
	}, nil
}

func (t *Tools) SearchContext(ctx context.Context, projectID string, in SearchContextInput) (*SearchContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required (set X-Project-ID header)")
	}
	if in.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}

	emb, err := t.embed.Embed(ctx, in.Query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vec := pgvector.NewVector(emb)

	// last_review_at: most recent review date, or updated_at if no reviews exist.
	// Used to compute freshness decay — a recent review resets the staleness clock.
	const searchCols = `
		cc.id, cc.project_id, cc.query_key, cc.title, cc.content, cc.embedding, cc.source_file,
		cc.source_lines, cc.gotchas, cc.related, cc.created_by_agent, cc.created_at, cc.updated_at,
		COALESCE((SELECT MAX(version) FROM context_versions WHERE chunk_id = cc.id), 1) AS version,
		COALESCE(1 - (cc.embedding <=> $1), 0) AS score,
		COALESCE(
			(SELECT MAX(cr.created_at) FROM context_reviews cr WHERE cr.chunk_id = cc.id),
			cc.updated_at
		) AS last_review_at`

	var rows pgxRows
	if in.QueryKey != "" {
		rows, err = pool(t).Query(ctx, `
			SELECT`+searchCols+`
			FROM context_chunks cc
			WHERE cc.project_id = $2 AND cc.query_key = $3
			ORDER BY (cc.embedding IS NULL), cc.embedding <=> $1
			LIMIT $4
		`, vec, projectID, in.QueryKey, limit)
	} else {
		rows, err = pool(t).Query(ctx, `
			SELECT`+searchCols+`
			FROM context_chunks cc
			WHERE cc.project_id = $2
			ORDER BY (cc.embedding IS NULL), cc.embedding <=> $1
			LIMIT $3
		`, vec, projectID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	results := []ChunkResult{}
	for rows.Next() {
		chunk, version, cosineScore, lastReviewAt, err := scanChunkSearchRow(rows)
		if err != nil {
			return nil, err
		}
		// lastActivity is the most recent of updated_at and the latest review.
		lastActivity := lastReviewAt
		if chunk.UpdatedAt.After(lastActivity) {
			lastActivity = chunk.UpdatedAt
		}
		freshness := computeFreshness(lastActivity)
		result := chunkResultFromModel(chunk, version, cosineScore*freshness)
		result.Freshness = freshness
		results = append(results, result)
	}

	// Re-sort by adjusted score (freshness already baked in). The SQL ORDER BY
	// used raw cosine distance, so the final ranking may shift slightly after decay.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return &SearchContextResult{Results: results}, nil
}

func (t *Tools) ReadContext(ctx context.Context, projectID, id string) (*ReadContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	row := pool(t).QueryRow(ctx, `
		SELECT cc.id, cc.project_id, cc.query_key, cc.title, cc.content, cc.embedding, cc.source_file,
		       cc.source_lines, cc.gotchas, cc.related, cc.created_by_agent, cc.created_at, cc.updated_at,
		       COALESCE((SELECT MAX(version) FROM context_versions WHERE chunk_id = cc.id), 1) AS version
		FROM context_chunks cc
		WHERE cc.id = $1 AND cc.project_id = $2
	`, id, projectID)

	chunk, currentVersion, err := scanChunkRow(row)
	if err != nil {
		return nil, fmt.Errorf("chunk not found: %w", err)
	}

	// Fetch versions
	vrows, err := pool(t).Query(ctx, `
		SELECT version, change_note, created_at FROM context_versions
		WHERE chunk_id = $1 ORDER BY version DESC
	`, chunk.ID)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()

	versions := []VersionResult{}
	for vrows.Next() {
		var v VersionResult
		if err := vrows.Scan(&v.Version, &v.ChangeNote, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}

	return &ReadContextResult{
		ChunkResult: chunkResultFromModel(chunk, currentVersion, 0),
		Versions:    versions,
	}, nil
}

func (t *Tools) UpdateContext(ctx context.Context, projectID string, in UpdateContextInput) (*UpdateContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if in.ChangeNote == "" {
		return nil, fmt.Errorf("change_note is required")
	}

	// Fetch current chunk
	row := pool(t).QueryRow(ctx, `
		SELECT content FROM context_chunks WHERE id = $1 AND project_id = $2
	`, in.ID, projectID)
	var currentContentB []byte
	if err := row.Scan(&currentContentB); err != nil {
		return nil, fmt.Errorf("chunk not found: %w", err)
	}

	// Get current version number
	currentVer, err := getVersion(ctx, pool(t), in.ID)
	if err != nil {
		return nil, err
	}
	newVer := currentVer + 1

	// Insert version record with old content
	_, err = pool(t).Exec(ctx, `
		INSERT INTO context_versions (chunk_id, version, content, change_note)
		VALUES ($1, $2, $3, $4)
	`, in.ID, newVer, currentContentB, in.ChangeNote)
	if err != nil {
		return nil, fmt.Errorf("insert version: %w", err)
	}

	// Build update query dynamically
	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argIdx := 1

	if in.Title != nil {
		setClauses = append(setClauses, fmt.Sprintf("title = $%d", argIdx))
		args = append(args, *in.Title)
		argIdx++
	}
	if in.Content != nil {
		newContentJSON := encodeContent(*in.Content)
		setClauses = append(setClauses, fmt.Sprintf("content = $%d", argIdx))
		args = append(args, newContentJSON)
		argIdx++

		// Re-embed
		emb, err := t.embed.Embed(ctx, *in.Content)
		if err != nil {
			log.Printf("re-embedding failed: %v", err)
		} else {
			vec := pgvector.NewVector(emb)
			setClauses = append(setClauses, fmt.Sprintf("embedding = $%d", argIdx))
			args = append(args, vec)
			argIdx++
		}
	}
	if in.SourceFile != nil {
		setClauses = append(setClauses, fmt.Sprintf("source_file = $%d", argIdx))
		args = append(args, *in.SourceFile)
		argIdx++
	}
	if len(in.SourceLines) >= 2 {
		slJSON, _ := json.Marshal(in.SourceLines[:2])
		setClauses = append(setClauses, fmt.Sprintf("source_lines = $%d", argIdx))
		args = append(args, slJSON)
		argIdx++
	}
	if in.Gotchas != nil {
		setClauses = append(setClauses, fmt.Sprintf("gotchas = $%d", argIdx))
		args = append(args, encodeStringSlice(in.Gotchas))
		argIdx++
	}
	if in.Related != nil {
		setClauses = append(setClauses, fmt.Sprintf("related = $%d", argIdx))
		args = append(args, encodeStringSlice(in.Related))
		argIdx++
	}

	// WHERE args
	args = append(args, in.ID, projectID)
	idIdx := argIdx
	projIdx := argIdx + 1

	query := fmt.Sprintf(`UPDATE context_chunks SET %s WHERE id = $%d AND project_id = $%d RETURNING updated_at`,
		joinClauses(setClauses), idIdx, projIdx)

	var updatedAt time.Time
	if err := pool(t).QueryRow(ctx, query, args...).Scan(&updatedAt); err != nil {
		return nil, fmt.Errorf("update chunk: %w", err)
	}

	return &UpdateContextResult{
		ID:        in.ID,
		Version:   newVer,
		UpdatedAt: updatedAt,
	}, nil
}

func (t *Tools) GetContextVersions(ctx context.Context, projectID, id string, limit int) (*GetVersionsResult, error) {
	if limit <= 0 {
		limit = 10
	}
	// Verify ownership
	var exists bool
	_ = pool(t).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_chunks WHERE id = $1 AND project_id = $2)`, id, projectID).Scan(&exists)
	if !exists {
		return nil, fmt.Errorf("chunk not found")
	}

	rows, err := pool(t).Query(ctx, `
		SELECT version, change_note, created_at FROM context_versions
		WHERE chunk_id = $1 ORDER BY version DESC LIMIT $2
	`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []VersionResult{}
	for rows.Next() {
		var v VersionResult
		if err := rows.Scan(&v.Version, &v.ChangeNote, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return &GetVersionsResult{Versions: versions}, nil
}

func (t *Tools) CompactContext(ctx context.Context, projectID string, in CompactContextInput) (*CompactContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if in.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	emb, err := t.embed.Embed(ctx, in.Query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vec := pgvector.NewVector(emb)

	rows, err := pool(t).Query(ctx, `
		SELECT cc.id, cc.project_id, cc.query_key, cc.title, cc.content, cc.embedding, cc.source_file,
		       cc.source_lines, cc.gotchas, cc.related, cc.created_by_agent, cc.created_at, cc.updated_at,
		       COALESCE((SELECT MAX(version) FROM context_versions WHERE chunk_id = cc.id), 1) AS version,
		       COALESCE(1 - (cc.embedding <=> $1), 0) AS score
		FROM context_chunks cc
		WHERE cc.project_id = $2
		ORDER BY (cc.embedding IS NULL), cc.embedding <=> $1
		LIMIT $3
	`, vec, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chunks := []ChunkResult{}
	for rows.Next() {
		chunk, version, score, err := scanChunkResultRow(rows)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunkResultFromModel(chunk, version, score))
	}
	return &CompactContextResult{Chunks: chunks, Total: len(chunks)}, nil
}

func (t *Tools) ReviewContext(ctx context.Context, projectID string, in ReviewContextInput) (*ReviewContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if in.ChunkID == "" {
		return nil, fmt.Errorf("chunk_id is required")
	}
	if in.Usefulness < 1 || in.Usefulness > 5 || in.Correctness < 1 || in.Correctness > 5 {
		return nil, fmt.Errorf("usefulness and correctness must be 1-5")
	}

	// Verify chunk belongs to project
	var exists bool
	_ = pool(t).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_chunks WHERE id = $1 AND project_id = $2)`, in.ChunkID, projectID).Scan(&exists)
	if !exists {
		return nil, fmt.Errorf("chunk not found")
	}

	var id string
	var createdAt time.Time
	err := pool(t).QueryRow(ctx, `
		INSERT INTO context_reviews (chunk_id, task, usefulness, usefulness_note, correctness, correctness_note, action)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, in.ChunkID, in.Task, in.Usefulness, in.UsefulnessNote, in.Correctness, in.CorrectnessNote, in.Action).
		Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert review: %w", err)
	}

	return &ReviewContextResult{
		ID:        id,
		ChunkID:   in.ChunkID,
		CreatedAt: createdAt,
	}, nil
}

func (t *Tools) DeleteContext(ctx context.Context, projectID, id string) (*DeleteContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	result, err := pool(t).Exec(ctx, `DELETE FROM context_chunks WHERE id = $1 AND project_id = $2`, id, projectID)
	if err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}
	deleted := result.RowsAffected() > 0
	return &DeleteContextResult{Deleted: deleted, ID: id}, nil
}

// ---- Helpers ----

// pool is a convenience accessor to avoid t.pool repetition
func pool(t *Tools) *pgxpool.Pool { return t.pool }

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func joinClauses(clauses []string) string {
	result := ""
	for i, c := range clauses {
		if i > 0 {
			result += ", "
		}
		result += c
	}
	return result
}

// pgxRows is an alias for the pgx rows interface used in queries.
type pgxRows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Close()
	Err() error
}

type pgxScanner interface {
	Scan(dest ...interface{}) error
}

func scanChunkRow(row pgxScanner) (models.ContextChunk, int, error) {
	var chunk models.ContextChunk
	var version int
	err := row.Scan(
		&chunk.ID,
		&chunk.ProjectID,
		&chunk.QueryKey,
		&chunk.Title,
		&chunk.Content,
		&chunk.Embedding,
		&chunk.SourceFile,
		&chunk.SourceLines,
		&chunk.Gotchas,
		&chunk.Related,
		&chunk.CreatedByAgent,
		&chunk.CreatedAt,
		&chunk.UpdatedAt,
		&version,
	)
	return chunk, version, err
}

func scanChunkResultRow(row pgxScanner) (models.ContextChunk, int, float64, error) {
	var chunk models.ContextChunk
	var version int
	var score float64
	err := row.Scan(
		&chunk.ID,
		&chunk.ProjectID,
		&chunk.QueryKey,
		&chunk.Title,
		&chunk.Content,
		&chunk.Embedding,
		&chunk.SourceFile,
		&chunk.SourceLines,
		&chunk.Gotchas,
		&chunk.Related,
		&chunk.CreatedByAgent,
		&chunk.CreatedAt,
		&chunk.UpdatedAt,
		&version,
		&score,
	)
	return chunk, version, score, err
}

// scanChunkSearchRow is like scanChunkResultRow but also scans the extra
// last_review_at column emitted by the SearchContext query.
func scanChunkSearchRow(row pgxScanner) (models.ContextChunk, int, float64, time.Time, error) {
	var chunk models.ContextChunk
	var version int
	var score float64
	var lastReviewAt time.Time
	err := row.Scan(
		&chunk.ID,
		&chunk.ProjectID,
		&chunk.QueryKey,
		&chunk.Title,
		&chunk.Content,
		&chunk.Embedding,
		&chunk.SourceFile,
		&chunk.SourceLines,
		&chunk.Gotchas,
		&chunk.Related,
		&chunk.CreatedByAgent,
		&chunk.CreatedAt,
		&chunk.UpdatedAt,
		&version,
		&score,
		&lastReviewAt,
	)
	return chunk, version, score, lastReviewAt, err
}

func chunkResultFromModel(chunk models.ContextChunk, version int, score float64) ChunkResult {
	sourceFile := ""
	if chunk.SourceFile != nil {
		sourceFile = *chunk.SourceFile
	}

	return ChunkResult{
		ID:          chunk.ID,
		QueryKey:    chunk.QueryKey,
		Title:       chunk.Title,
		Content:     decodeContent(chunk.Content),
		SourceFile:  sourceFile,
		SourceLines: decodeSourceLines(chunk.SourceLines),
		Gotchas:     decodeStringSlice(chunk.Gotchas),
		Related:     decodeStringSlice(chunk.Related),
		Score:       score,
		Version:     version,
		CreatedAt:   chunk.CreatedAt,
		UpdatedAt:   chunk.UpdatedAt,
	}
}
