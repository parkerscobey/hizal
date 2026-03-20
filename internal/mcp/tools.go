package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/XferOps/winnow/internal/billing"

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

// ---- Input types for purpose-built write tools ----

type WriteIdentityInput struct {
	AgentID        string                    `json:"agent_id"`
	QueryKey       string                    `json:"query_key"`
	Title          string                    `json:"title"`
	Content        string                    `json:"content"`
	SourceFile     string                    `json:"source_file,omitempty"`
	SourceLines    [2]int                    `json:"source_lines,omitempty"`
	Gotchas        []string                  `json:"gotchas,omitempty"`
	Related        []string                  `json:"related,omitempty"`
	InjectAudience *models.InjectAudience    `json:"inject_audience,omitempty"`
	Visibility     string                    `json:"visibility,omitempty"`
}

type WriteMemoryInput struct {
	AgentID        string                    `json:"agent_id"`
	QueryKey       string                    `json:"query_key"`
	Title          string                    `json:"title"`
	Content        string                    `json:"content"`
	SourceFile     string                    `json:"source_file,omitempty"`
	SourceLines    [2]int                    `json:"source_lines,omitempty"`
	Gotchas        []string                  `json:"gotchas,omitempty"`
	Related        []string                  `json:"related,omitempty"`
	InjectAudience *models.InjectAudience    `json:"inject_audience,omitempty"`
	Visibility     string                    `json:"visibility,omitempty"`
}

type WriteKnowledgeInput struct {
	ProjectID      string                    `json:"project_id"`
	QueryKey       string                    `json:"query_key"`
	Title          string                    `json:"title"`
	Content        string                    `json:"content"`
	SourceFile     string                    `json:"source_file,omitempty"`
	SourceLines    [2]int                    `json:"source_lines,omitempty"`
	Gotchas        []string                  `json:"gotchas,omitempty"`
	Related        []string                  `json:"related,omitempty"`
	InjectAudience *models.InjectAudience    `json:"inject_audience,omitempty"`
	Visibility     string                    `json:"visibility,omitempty"`
}

type WriteConventionInput struct {
	ProjectID      string                    `json:"project_id"`
	QueryKey       string                    `json:"query_key"`
	Title          string                    `json:"title"`
	Content        string                    `json:"content"`
	SourceFile     string                    `json:"source_file,omitempty"`
	SourceLines    [2]int                    `json:"source_lines,omitempty"`
	Gotchas        []string                  `json:"gotchas,omitempty"`
	Related        []string                  `json:"related,omitempty"`
	InjectAudience *models.InjectAudience    `json:"inject_audience,omitempty"`
	Visibility     string                    `json:"visibility,omitempty"`
}

type WriteOrgKnowledgeInput struct {
	OrgID          string                    `json:"org_id"`
	QueryKey       string                    `json:"query_key"`
	Title          string                    `json:"title"`
	Content        string                    `json:"content"`
	SourceFile     string                    `json:"source_file,omitempty"`
	SourceLines    [2]int                    `json:"source_lines,omitempty"`
	Gotchas        []string                  `json:"gotchas,omitempty"`
	Related        []string                  `json:"related,omitempty"`
	InjectAudience *models.InjectAudience    `json:"inject_audience,omitempty"`
	Visibility     string                    `json:"visibility,omitempty"`
}

type StorePrincipleInput struct {
	OrgID            string                    `json:"org_id"`
	QueryKey         string                    `json:"query_key"`
	Title            string                    `json:"title"`
	Content          string                    `json:"content"`
	PromotedByUserID string                    `json:"promoted_by_user_id"`
	SourceFile       string                    `json:"source_file,omitempty"`
	SourceLines      [2]int                    `json:"source_lines,omitempty"`
	Gotchas          []string                  `json:"gotchas,omitempty"`
	Related          []string                  `json:"related,omitempty"`
	InjectAudience   *models.InjectAudience    `json:"inject_audience,omitempty"`
	Visibility       string                    `json:"visibility,omitempty"`
}

type WriteChunkInput struct {
	Type            string           `json:"type"`
	QueryKey        string           `json:"query_key"`
	Title           string           `json:"title"`
	Content         string           `json:"content"`
	ProjectID       string           `json:"project_id,omitempty"`
	AgentID         string           `json:"agent_id,omitempty"`
	OrgID           string           `json:"org_id,omitempty"`
	InjectAudience  *json.RawMessage `json:"inject_audience,omitempty"`
	Scope           string           `json:"scope,omitempty"`
	SourceFile      string           `json:"source_file,omitempty"`
	SourceLines     [2]int           `json:"source_lines,omitempty"`
	Gotchas         []string         `json:"gotchas,omitempty"`
	Related         []string         `json:"related,omitempty"`
	Visibility      string           `json:"visibility,omitempty"`
}

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
	ProjectID string `json:"project_id,omitempty"`
	// Scope is PROJECT | AGENT | ORG. Defaults to PROJECT.
	// Prefer purpose-built tools (write_knowledge, write_memory, etc.) over
	// setting scope manually — they route correctly and enforce guardrails.
	Scope string `json:"scope,omitempty"`
	// AgentID is required when Scope is AGENT.
	AgentID string `json:"agent_id,omitempty"`
	// OrgID is required when Scope is ORG.
	OrgID string `json:"org_id,omitempty"`
	// InjectAudience: JSONB targeting spec. nil = on-demand only.
	InjectAudience *json.RawMessage `json:"inject_audience,omitempty"`
	// ChunkType: IDENTITY | MEMORY | KNOWLEDGE | CONVENTION | PRINCIPLE | DECISION | RESEARCH | PLAN | SPEC | IMPLEMENTATION | CONSTRAINT | LESSON. Defaults to KNOWLEDGE. Must be a valid type for the org (global or org-specific).
	ChunkType   string   `json:"chunk_type,omitempty"`
	QueryKey    string   `json:"query_key"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	SourceFile  string   `json:"source_file,omitempty"`
	SourceLines [2]int   `json:"source_lines,omitempty"`
	Gotchas     []string `json:"gotchas,omitempty"`
	Related     []string `json:"related,omitempty"`
	// Visibility controls public hub discoverability. "private" (default) | "public".
	// Public chunks are never auto-injected — only discoverable on the hub.
	Visibility string `json:"visibility,omitempty"`
}

type WriteContextResult struct {
	ID              string                  `json:"id"`
	Scope           string                  `json:"scope"`
	InjectAudience  *models.InjectAudience   `json:"inject_audience"`
	ChunkType       string                  `json:"chunk_type"`
	QueryKey        string                  `json:"query_key"`
	Title           string                  `json:"title"`
	Visibility      string                  `json:"visibility,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
}

type SearchContextInput struct {
	ProjectID string `json:"project_id,omitempty"`
	// Scope filters results to a specific scope. If empty, searches all accessible scopes.
	// Values: PROJECT | AGENT | ORG
	Scope string `json:"scope,omitempty"`
	// AgentID filters results to a specific agent (for AGENT-scoped chunks).
	AgentID string `json:"agent_id,omitempty"`
	// OrgID filters results to org-scoped chunks. If empty, derived from API key.
	OrgID    string `json:"org_id,omitempty"`
	Query    string `json:"query"`
	Limit    int    `json:"limit,omitempty"`
	QueryKey string `json:"query_key,omitempty"`
	// ChunkType filters by chunk_type (KNOWLEDGE, MEMORY, CONVENTION, IDENTITY, PRINCIPLE).
	ChunkType string `json:"chunk_type,omitempty"`
	// AlwaysInjectOnly filters to only always_inject=true chunks.
	AlwaysInjectOnly bool `json:"always_inject_only,omitempty"`
}

// StaleSignal represents a review that flagged a chunk as potentially stale.
// Explicit non-keep actions (needs_update, outdated, incorrect) and low rating
// scores (usefulness < 3 or correctness < 3) both produce signals.
type StaleSignal struct {
	// Action is the review action: "needs_update", "outdated", "incorrect", etc.
	// Set to "low_score" when a score-based signal fires without an explicit action.
	Action string `json:"action"`
	// Note is the most relevant reviewer note (correctness_note preferred, then usefulness_note).
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ChunkResult struct {
	ID             string                  `json:"id"`
	Scope          string                  `json:"scope"`
	AgentID        *string                 `json:"agent_id,omitempty"`
	OrgID          *string                 `json:"org_id,omitempty"`
	InjectAudience *models.InjectAudience  `json:"inject_audience,omitempty"`
	ChunkType      string                  `json:"chunk_type"`
	QueryKey       string                  `json:"query_key"`
	Title          string                  `json:"title"`
	Content        string                  `json:"content"`
	SourceFile     string                  `json:"source_file,omitempty"`
	SourceLines    []int                   `json:"source_lines,omitempty"`
	Gotchas        []string                `json:"gotchas,omitempty"`
	Related        []string                `json:"related,omitempty"`
	Visibility     string                  `json:"visibility,omitempty"`
	Score          float64                 `json:"score,omitempty"`
	Freshness      float64                 `json:"freshness,omitempty"`
	StaleSignals   []StaleSignal           `json:"stale_signals,omitempty"`
	Version        int                     `json:"version,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

type SearchContextResult struct {
	Results []ChunkResult `json:"results"`
}

type ReadContextInput struct {
	ProjectID string `json:"project_id,omitempty"`
	ID        string `json:"id,omitempty"`
	QueryKey  string `json:"query_key,omitempty"`
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
	ProjectID      string           `json:"project_id,omitempty"`
	ID             string           `json:"id"`
	Title          *string          `json:"title,omitempty"`
	Content        *string          `json:"content,omitempty"`
	SourceFile     *string          `json:"source_file,omitempty"`
	SourceLines    []int            `json:"source_lines,omitempty"`
	Gotchas        []string         `json:"gotchas,omitempty"`
	Related        []string         `json:"related,omitempty"`
	ChangeNote     string           `json:"change_note"`
	InjectAudience *json.RawMessage `json:"inject_audience,omitempty"`
	Visibility     *string          `json:"visibility,omitempty"`
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
	Scope     string `json:"scope,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
	ChunkType string `json:"chunk_type,omitempty"`
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

func isValidChunkType(ctx context.Context, pool *pgxpool.Pool, orgID string, chunkType string) (bool, error) {
	var count int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM chunk_types
		WHERE slug = $1 AND (org_id IS NULL OR org_id = $2)
	`, chunkType, orgID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

type ChunkTypeDefaults struct {
	DefaultScope             string
	DefaultInjectAudience    *models.InjectAudience
}

func resolveChunkTypeDefaults(ctx context.Context, pool *pgxpool.Pool, orgID *string, slug string) (ChunkTypeDefaults, error) {
	var defaults ChunkTypeDefaults
	var iaRaw []byte
	err := pool.QueryRow(ctx, `
		SELECT default_scope, default_inject_audience
		FROM chunk_types
		WHERE slug = $1 AND (org_id IS NULL OR org_id = $2)
		ORDER BY org_id NULLS LAST
		LIMIT 1
	`, slug, nullStrPtr(orgID)).Scan(&defaults.DefaultScope, &iaRaw)
	if err != nil {
		return defaults, fmt.Errorf("chunk type %q not found: %w", slug, err)
	}
	if len(iaRaw) > 0 {
		var ia models.InjectAudience
		if err := json.Unmarshal(iaRaw, &ia); err == nil {
			defaults.DefaultInjectAudience = &ia
		}
	}
	return defaults, nil
}

func nullStrPtr(s *string) interface{} {
	if s == nil {
		return nil
	}
	return *s
}

func effectiveInjectAudience(override *models.InjectAudience, defaultIA *models.InjectAudience) *models.InjectAudience {
	if override != nil {
		return override
	}
	return defaultIA
}

func normalizeVisibility(v string) string {
	if v == "public" {
		return "public"
	}
	return "private"
}

// ---- Tool Implementations ----

func (t *Tools) WriteContext(ctx context.Context, projectID string, in WriteContextInput) (*WriteContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required (set X-Project-ID header)")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	// Check project is not locked (downgraded)
	var lockedAt *time.Time
	if err := pool(t).QueryRow(ctx, `SELECT locked_at FROM projects WHERE id = $1`, projectID).Scan(&lockedAt); err == nil && lockedAt != nil {
		return nil, fmt.Errorf("PROJECT_LOCKED: this project is read-only — upgrade to Pro to unlock it")
	}

	// Enforce org-scoped chunk limit
	var tier string
	var chunkCount int
	pool(t).QueryRow(ctx, `SELECT o.tier FROM orgs o JOIN projects p ON p.org_id = o.id WHERE p.id = $1`, projectID).Scan(&tier)
	pool(t).QueryRow(ctx, `
		SELECT COUNT(*) FROM context_chunks cc
		JOIN projects p ON p.id = cc.project_id
		WHERE p.org_id = (SELECT org_id FROM projects WHERE id = $1)
	`, projectID).Scan(&chunkCount)

	limits := billing.For(tier)
	if limits.ChunkLimit >= 0 && chunkCount >= limits.ChunkLimit {
		return nil, fmt.Errorf("CHUNK_LIMIT_REACHED: you've used all %d chunks on the %s plan — upgrade to write more context", limits.ChunkLimit, tier)
	}

	emb, err := t.embed.Embed(ctx, in.Content)
	if err != nil {
		log.Printf("embedding failed: %v", err)
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}

	// Resolve scope — default to PROJECT for backward compatibility.
	scope := in.Scope
	if scope == "" {
		scope = "PROJECT"
	}

	// Resolve always_inject — default to false for backward compatibility.
	effectiveInjectAudience := resolveInjectAudience(in.InjectAudience)

	// Resolve chunk_type — default to KNOWLEDGE.
	chunkType := in.ChunkType
	if chunkType == "" {
		chunkType = "KNOWLEDGE"
	}

	// Validate chunk_type against chunk_types table
	var orgID string
	pool(t).QueryRow(ctx, `SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&orgID)
	valid, err := isValidChunkType(ctx, pool(t), orgID, chunkType)
	if err != nil {
		return nil, fmt.Errorf("chunk_type validation failed: %w", err)
	}
	if !valid {
		return nil, fmt.Errorf("INVALID_CHUNK_TYPE: %q is not a valid chunk type for this org", chunkType)
	}

	contentJSON := encodeContent(in.Content)
	gotchasJSON := encodeStringSlice(in.Gotchas)
	relatedJSON := encodeStringSlice(in.Related)
	sourceLinesJSON := encodeSourceLines(in.SourceLines)
	vec := pgvector.NewVector(emb)

	vis := normalizeVisibility(in.Visibility)

	var id string
	var createdAt time.Time
	err = pool(t).QueryRow(ctx, `
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, created_at
	`, nullStr(projectID), scope, nullStr(in.AgentID), nullStr(in.OrgID),
		nullInjectAudience(effectiveInjectAudience), vis, chunkType,
		in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        scope,
		InjectAudience: effectiveInjectAudience,
		ChunkType:    chunkType,
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

func (t *Tools) SearchContext(ctx context.Context, projectID string, in SearchContextInput, typeFilters models.AgentTypeFilterConfig) (*SearchContextResult, error) {
	if in.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}

	// Resolve scope context. in.ProjectID overrides the header projectID if set.
	effectiveProjectID := projectID
	if in.ProjectID != "" {
		effectiveProjectID = in.ProjectID
	}
	agentID := in.AgentID
	orgID := in.OrgID

	emb, err := t.embed.Embed(ctx, in.Query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vec := pgvector.NewVector(emb)

	effectiveScope, effectiveChunkType := applyTypeFilters(in.Scope, in.ChunkType, typeFilters)

	// Build scope-aware WHERE clause.
	args := []interface{}{vec}
	scopeClause, args := scopeFilter(effectiveScope, effectiveProjectID, agentID, orgID, args)
	typeClause, args := chunkTypeFilter(effectiveChunkType, args)
	injectClause, args := alwaysInjectFilter(in.AlwaysInjectOnly, args)
	var excludeQKPClause string
	if len(typeFilters.ExcludeQueryKeyPrefixes) > 0 {
		excludeQKPClause = excludeQueryKeyPrefixesClause(typeFilters.ExcludeQueryKeyPrefixes)
	}

	// last_review_at: most recent review date, or updated_at if no reviews exist.
	const searchCols = `cc.id, cc.project_id, cc.query_key, cc.title, cc.content, cc.embedding::text, cc.source_file,
			cc.source_lines, cc.gotchas, cc.related, cc.created_by_agent, cc.created_at, cc.updated_at,
			COALESCE((SELECT MAX(version) FROM context_versions WHERE chunk_id = cc.id), 1) AS version,
			COALESCE(1 - (cc.embedding <=> $1), 0) AS score,
			COALESCE(
				(SELECT MAX(cr.created_at) FROM context_reviews cr WHERE cr.chunk_id = cc.id),
				cc.updated_at
			) AS last_review_at,
			cc.visibility`

	var rows pgxRows
	if in.QueryKey != "" {
		args = append(args, in.QueryKey)
		qkIdx := len(args)
		args = append(args, limit)
		limIdx := len(args)
		query := fmt.Sprintf(`
			SELECT %s
			FROM context_chunks cc
			WHERE cc.query_key = $%d
			%s %s %s %s
			ORDER BY (cc.embedding IS NULL), cc.embedding <=> $1
			LIMIT $%d
		`, searchCols, qkIdx, scopeClause, typeClause, injectClause, excludeQKPClause, limIdx)
		rows, err = pool(t).Query(ctx, query, args...)
	} else {
		args = append(args, limit)
		limIdx := len(args)
		query := fmt.Sprintf(`
			SELECT %s
			FROM context_chunks cc
			WHERE TRUE
			%s %s %s %s
			ORDER BY (cc.embedding IS NULL), cc.embedding <=> $1
			LIMIT $%d
		`, searchCols, scopeClause, typeClause, injectClause, excludeQKPClause, limIdx)
		rows, err = pool(t).Query(ctx, query, args...)
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
	// Note: SQL LIMIT is applied before freshness decay, so decay re-ranks within
	// the top-N window rather than across the full result set.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Attach any stale signals (non-keep review actions + low scores) in one
	// batch query so agents know which chunks may need attention.
	chunkIDs := make([]string, len(results))
	for i, r := range results {
		chunkIDs[i] = r.ID
	}
	signals, err := t.fetchStaleSignals(ctx, chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch stale signals: %w", err)
	}
	for i, r := range results {
		if ss := signals[r.ID]; len(ss) > 0 {
			results[i].StaleSignals = ss
		}
	}

	return &SearchContextResult{Results: results}, nil
}

func (t *Tools) ReadContext(ctx context.Context, projectID string, in ReadContextInput) (*ReadContextResult, error) {
	if in.ID == "" && in.QueryKey == "" {
		return nil, fmt.Errorf("id or query_key is required")
	}

	query := `
		SELECT cc.id, cc.project_id, cc.scope, cc.agent_id, cc.org_id, cc.inject_audience, cc.visibility, cc.chunk_type,
		       cc.query_key, cc.title, cc.content, cc.embedding::text, cc.source_file, cc.source_lines,
		       cc.gotchas, cc.related, cc.created_by_agent, cc.created_at, cc.updated_at,
		       COALESCE((SELECT MAX(version) FROM context_versions WHERE chunk_id = cc.id), 1) AS version
		FROM context_chunks cc
	`

	var row pgxScanner
	if in.ID != "" {
		// Scope-aware read: look up by chunk ID only. project_id is optional context
		// for backward compatibility but NOT used as an access gate — chunk ID is globally unique.
		row = pool(t).QueryRow(ctx, query+`WHERE cc.id = $1`, in.ID)
	} else {
		if projectID == "" {
			return nil, fmt.Errorf("project_id is required when reading by query_key")
		}
		row = pool(t).QueryRow(ctx, query+`WHERE cc.query_key = $1 AND cc.project_id = $2 LIMIT 1`, in.QueryKey, projectID)
	}

	chunk, currentVersion, err := scanChunkReadRow(row)
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
		ChunkResult: readContextResultFromModel(chunk, currentVersion),
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

	// Fetch current chunk — scope-aware: look up by ID only (chunk IDs are globally unique).
	row := pool(t).QueryRow(ctx, `
		SELECT content FROM context_chunks WHERE id = $1
	`, in.ID)
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
	if in.InjectAudience != nil {
		setClauses = append(setClauses, fmt.Sprintf("inject_audience = $%d", argIdx))
		args = append(args, *in.InjectAudience)
		argIdx++
	}
	if in.Visibility != nil {
		setClauses = append(setClauses, fmt.Sprintf("visibility = $%d", argIdx))
		args = append(args, normalizeVisibility(*in.Visibility))
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

	if in.InjectAudience != nil {
		_, _ = pool(t).Exec(ctx, `
			UPDATE sessions
			SET inject_set = NULL, updated_at = NOW()
			WHERE inject_set @> $1::jsonb
			  AND status = 'active'
		`, fmt.Sprintf(`["%s"]`, in.ID))
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
	_ = pool(t).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_chunks WHERE id = $1)`, id).Scan(&exists)
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

func (t *Tools) CompactContext(ctx context.Context, projectID string, in CompactContextInput, typeFilters models.AgentTypeFilterConfig) (*CompactContextResult, error) {
	if in.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	effectiveProjectID := projectID
	if in.ProjectID != "" {
		effectiveProjectID = in.ProjectID
	}

	emb, err := t.embed.Embed(ctx, in.Query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vec := pgvector.NewVector(emb)

	effectiveScope, effectiveChunkType := applyTypeFilters(in.Scope, in.ChunkType, typeFilters)

	args := []interface{}{vec}
	scopeClause, args := scopeFilter(effectiveScope, effectiveProjectID, in.AgentID, in.OrgID, args)
	typeClause, args := chunkTypeFilter(effectiveChunkType, args)
	excludeQKPClause := excludeQueryKeyPrefixesClause(typeFilters.ExcludeQueryKeyPrefixes)
	args = append(args, limit)
	limIdx := len(args)

	query := fmt.Sprintf(`
		SELECT cc.id, cc.project_id, cc.query_key, cc.title, cc.content, cc.embedding::text, cc.source_file,
		       cc.source_lines, cc.gotchas, cc.related, cc.created_by_agent, cc.created_at, cc.updated_at,
		       COALESCE((SELECT MAX(version) FROM context_versions WHERE chunk_id = cc.id), 1) AS version,
		       COALESCE(1 - (cc.embedding <=> $1), 0) AS score,
		       cc.visibility
		FROM context_chunks cc
		WHERE TRUE
		%s %s %s
		ORDER BY (cc.embedding IS NULL), cc.embedding <=> $1
		LIMIT $%d
	`, scopeClause, typeClause, excludeQKPClause, limIdx)

	rows, err := pool(t).Query(ctx, query, args...)
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

	// Verify chunk exists (scope-aware: ID is globally unique, no project_id gate)
	var exists bool
	_ = pool(t).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_chunks WHERE id = $1)`, in.ChunkID).Scan(&exists)
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
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	// Scope-aware delete: chunk IDs are globally unique — no project_id gate needed.
	result, err := pool(t).Exec(ctx, `DELETE FROM context_chunks WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}
	deleted := result.RowsAffected() > 0
	return &DeleteContextResult{Deleted: deleted, ID: id}, nil
}

// maxStaleSignalsPerChunk caps how many stale signals are returned per chunk
// in search results. Agents need to know that a chunk is stale and why — they
// don't need an exhaustive history. Most-recent signals are kept (SQL orders
// by created_at DESC before this limit is applied).
const maxStaleSignalsPerChunk = 5

// fetchStaleSignals returns a map of chunk ID → stale signals for a batch of
// chunk IDs. A signal is generated when a review has a non-keep action (e.g.
// "needs_update", "outdated", "incorrect") or a low rating (usefulness < 3 or
// correctness < 3). One query fetches all signals for the full result set.
func (t *Tools) fetchStaleSignals(ctx context.Context, chunkIDs []string) (map[string][]StaleSignal, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	rows, err := pool(t).Query(ctx, `
		SELECT
			chunk_id,
			CASE
				WHEN action IS NOT NULL AND action <> '' AND action <> 'keep' THEN action
				ELSE 'low_score'
			END AS action,
			COALESCE(NULLIF(correctness_note, ''), NULLIF(usefulness_note, ''), '') AS note,
			created_at
		FROM context_reviews
		WHERE chunk_id = ANY($1)
		  AND (
		    (action IS NOT NULL AND action <> '' AND action <> 'keep')
		    OR usefulness < 3
		    OR correctness < 3
		  )
		ORDER BY created_at DESC
	`, chunkIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]StaleSignal{}
	for rows.Next() {
		var chunkID, action, note string
		var createdAt time.Time
		if err := rows.Scan(&chunkID, &action, &note, &createdAt); err != nil {
			return nil, err
		}
		// Only accumulate up to maxStaleSignalsPerChunk per chunk — we have
		// everything we need to surface staleness without unbounded growth.
		if len(result[chunkID]) < maxStaleSignalsPerChunk {
			result[chunkID] = append(result[chunkID], StaleSignal{
				Action:    action,
				Note:      note,
				CreatedAt: createdAt,
			})
		}
	}
	return result, rows.Err()
}

// ---- Purpose-Built Write Tools ----

// WriteIdentity stores an IDENTITY chunk scoped to an agent.
// scope and always_inject are derived from the chunk_types table.
// The type slug (IDENTITY) is always enforced regardless of table defaults.
func (t *Tools) WriteIdentity(ctx context.Context, in WriteIdentityInput) (*WriteContextResult, error) {
	if in.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), nil, "IDENTITY")
	if err != nil {
		return nil, fmt.Errorf("resolve chunk type defaults: %w", err)
	}

	effectiveIA := effectiveInjectAudience(in.InjectAudience, defaults.DefaultInjectAudience)
	vis := normalizeVisibility(in.Visibility)

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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES (NULL, $1, $2, $3, $4, $5, 'IDENTITY', $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at
	`, defaults.DefaultScope, in.AgentID, nullInjectAudience(effectiveIA), vis, in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        defaults.DefaultScope,
		InjectAudience: effectiveIA,
		ChunkType:    "IDENTITY",
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

// WriteMemory stores a MEMORY chunk scoped to an agent.
// scope and always_inject are derived from the chunk_types table.
// The type slug (MEMORY) is always enforced regardless of table defaults.
func (t *Tools) WriteMemory(ctx context.Context, in WriteMemoryInput) (*WriteContextResult, error) {
	if in.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), nil, "MEMORY")
	if err != nil {
		return nil, fmt.Errorf("resolve chunk type defaults: %w", err)
	}

	effectiveIA := effectiveInjectAudience(in.InjectAudience, defaults.DefaultInjectAudience)
	vis := normalizeVisibility(in.Visibility)

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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES (NULL, $1, $2, NULL, $3, $4, 'MEMORY', $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at
	`, defaults.DefaultScope, in.AgentID, nullInjectAudience(effectiveIA), vis, in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        defaults.DefaultScope,
		InjectAudience: effectiveIA,
		ChunkType:    "MEMORY",
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

// WriteKnowledge stores a KNOWLEDGE chunk scoped to a project.
// scope and always_inject are derived from the chunk_types table.
// The type slug (KNOWLEDGE) is always enforced regardless of table defaults.
func (t *Tools) WriteKnowledge(ctx context.Context, projectID string, in WriteKnowledgeInput) (*WriteContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	// Check project is not locked (downgraded)
	var lockedAt *time.Time
	if err := pool(t).QueryRow(ctx, `SELECT locked_at FROM projects WHERE id = $1`, projectID).Scan(&lockedAt); err == nil && lockedAt != nil {
		return nil, fmt.Errorf("PROJECT_LOCKED: this project is read-only — upgrade to Pro to unlock it")
	}

	var orgID *string
	pool(t).QueryRow(ctx, `SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&orgID)

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), orgID, "KNOWLEDGE")
	if err != nil {
		return nil, fmt.Errorf("resolve chunk type defaults: %w", err)
	}

	effectiveIA := effectiveInjectAudience(in.InjectAudience, defaults.DefaultInjectAudience)
	vis := normalizeVisibility(in.Visibility)

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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES ($1, $2, NULL, NULL, $3, $4, 'KNOWLEDGE', $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at
	`, projectID, defaults.DefaultScope, nullInjectAudience(effectiveIA), vis, in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        defaults.DefaultScope,
		InjectAudience: effectiveIA,
		ChunkType:    "KNOWLEDGE",
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

// WriteConvention stores a CONVENTION chunk scoped to a project.
// scope and always_inject are derived from the chunk_types table.
// The type slug (CONVENTION) is always enforced regardless of table defaults.
func (t *Tools) WriteConvention(ctx context.Context, projectID string, in WriteConventionInput) (*WriteContextResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	// Check project is not locked (downgraded)
	var lockedAt *time.Time
	if err := pool(t).QueryRow(ctx, `SELECT locked_at FROM projects WHERE id = $1`, projectID).Scan(&lockedAt); err == nil && lockedAt != nil {
		return nil, fmt.Errorf("PROJECT_LOCKED: this project is read-only — upgrade to Pro to unlock it")
	}

	var orgID *string
	pool(t).QueryRow(ctx, `SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&orgID)

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), orgID, "CONVENTION")
	if err != nil {
		return nil, fmt.Errorf("resolve chunk type defaults: %w", err)
	}

	effectiveIA := effectiveInjectAudience(in.InjectAudience, defaults.DefaultInjectAudience)
	vis := normalizeVisibility(in.Visibility)

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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES ($1, $2, NULL, NULL, $3, $4, 'CONVENTION', $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at
	`, projectID, defaults.DefaultScope, nullInjectAudience(effectiveIA), vis, in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        defaults.DefaultScope,
		InjectAudience: effectiveIA,
		ChunkType:    "CONVENTION",
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

// WriteOrgKnowledge stores a KNOWLEDGE chunk scoped to an org.
// scope and always_inject are derived from the chunk_types table.
// The type slug (KNOWLEDGE) is always enforced regardless of table defaults.
func (t *Tools) WriteOrgKnowledge(ctx context.Context, orgID string, in WriteOrgKnowledgeInput) (*WriteContextResult, error) {
	if orgID == "" {
		return nil, fmt.Errorf("org_id is required")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), &orgID, "KNOWLEDGE")
	if err != nil {
		return nil, fmt.Errorf("resolve chunk type defaults: %w", err)
	}

	effectiveIA := effectiveInjectAudience(in.InjectAudience, defaults.DefaultInjectAudience)
	vis := normalizeVisibility(in.Visibility)

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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES (NULL, 'ORG', NULL, $1, $2, $3, 'KNOWLEDGE', $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at
	`, orgID, nullInjectAudience(effectiveIA), vis, in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        "ORG",
		InjectAudience: effectiveIA,
		ChunkType:    "KNOWLEDGE",
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

// StorePrinciple stores a PRINCIPLE chunk scoped to an org.
// scope and always_inject are derived from the chunk_types table.
// The type slug (PRINCIPLE) is always enforced regardless of table defaults.
// Requires promoted_by_user_id to enforce human promotion.
func (t *Tools) StorePrinciple(ctx context.Context, orgID string, in StorePrincipleInput) (*WriteContextResult, error) {
	if orgID == "" {
		return nil, fmt.Errorf("org_id is required")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}
	if in.PromotedByUserID == "" {
		return nil, fmt.Errorf("store_principle requires human promotion — use write_org_knowledge to propose, then a human promotes via the API.")
	}

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), &orgID, "PRINCIPLE")
	if err != nil {
		return nil, fmt.Errorf("resolve chunk type defaults: %w", err)
	}

	effectiveIA := effectiveInjectAudience(in.InjectAudience, defaults.DefaultInjectAudience)
	vis := normalizeVisibility(in.Visibility)

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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES (NULL, 'ORG', NULL, $1, $2, $3, 'PRINCIPLE', $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at
	`, orgID, nullInjectAudience(effectiveIA), vis, in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        "ORG",
		InjectAudience: effectiveIA,
		ChunkType:    "PRINCIPLE",
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
}

// WriteChunk is the generic chunk writing tool. It looks up the type's scope
// and always_inject defaults from the chunk_types table, then applies any
// overrides provided by the caller. This is the path for custom org chunk types.
// The six named tools (write_identity, write_memory, etc.) remain for the 12
// global defaults — they're opinionated shortcuts that guarantee correct semantics.
func (t *Tools) WriteChunk(ctx context.Context, projectID string, in WriteChunkInput) (*WriteContextResult, error) {
	if in.Type == "" {
		return nil, fmt.Errorf("type is required (e.g. KNOWLEDGE, MEMORY, SPEC)")
	}
	if in.QueryKey == "" || in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("query_key, title, and content are required")
	}

	// Determine orgID for chunk_types lookup
	var orgID *string
	if projectID != "" {
		pool(t).QueryRow(ctx, `SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&orgID)
	} else if in.OrgID != "" {
		orgID = &in.OrgID
	}

	defaults, err := resolveChunkTypeDefaults(ctx, pool(t), orgID, in.Type)
	if err != nil {
		return nil, fmt.Errorf("INVALID_CHUNK_TYPE: %q is not a valid chunk type for this org", in.Type)
	}

	// Resolve scope — override wins, else table default
	effectiveScope := in.Scope
	if effectiveScope == "" {
		effectiveScope = defaults.DefaultScope
	}

	// Resolve inject_audience — override wins, else table default
	effectiveInjectAudience := defaults.DefaultInjectAudience
	if in.InjectAudience != nil {
		effectiveInjectAudience = resolveInjectAudience(in.InjectAudience)
	}

	vis := normalizeVisibility(in.Visibility)

	// Validate required IDs based on effective scope
	var effectiveProjectID *string
	var effectiveAgentID *string
	var effectiveOrgID *string

	switch effectiveScope {
	case "PROJECT":
		if projectID == "" && in.ProjectID == "" {
			return nil, fmt.Errorf("project_id is required for PROJECT-scoped chunks")
		}
		effectiveProjectID = &projectID
		if in.ProjectID != "" {
			effectiveProjectID = &in.ProjectID
		}
	case "AGENT":
		if in.AgentID == "" {
			return nil, fmt.Errorf("agent_id is required for AGENT-scoped chunks")
		}
		effectiveAgentID = &in.AgentID
	case "ORG":
		if in.OrgID == "" {
			return nil, fmt.Errorf("org_id is required for ORG-scoped chunks")
		}
		effectiveOrgID = &in.OrgID
	}

	// Check project is not locked (downgraded)
	if effectiveProjectID != nil && *effectiveProjectID != "" {
		var lockedAt *time.Time
		if err := pool(t).QueryRow(ctx, `SELECT locked_at FROM projects WHERE id = $1`, *effectiveProjectID).Scan(&lockedAt); err == nil && lockedAt != nil {
			return nil, fmt.Errorf("PROJECT_LOCKED: this project is read-only — upgrade to Pro to unlock it")
		}
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
		INSERT INTO context_chunks (project_id, scope, agent_id, org_id, inject_audience, visibility, chunk_type, query_key, title, content, embedding, source_file, source_lines, gotchas, related)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, created_at
	`, effectiveProjectID, effectiveScope, effectiveAgentID, effectiveOrgID,
		nullInjectAudience(effectiveInjectAudience), vis, in.Type,
		in.QueryKey, in.Title, contentJSON, vec,
		nullStr(in.SourceFile), sourceLinesJSON, gotchasJSON, relatedJSON).
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
		ID:           id,
		Scope:        effectiveScope,
		InjectAudience: effectiveInjectAudience,
		ChunkType:    in.Type,
		QueryKey:     in.QueryKey,
		Title:        in.Title,
		Visibility:   vis,
		CreatedAt:    createdAt,
	}, nil
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

func resolveInjectAudience(raw *json.RawMessage) *models.InjectAudience {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil
	}
	var ia models.InjectAudience
	if err := json.Unmarshal(*raw, &ia); err != nil {
		return nil
	}
	return &ia
}

func nullInjectAudience(ia *models.InjectAudience) interface{} {
	if ia == nil {
		return nil
	}
	b, err := json.Marshal(ia)
	if err != nil {
		return nil
	}
	return string(b)
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

// scopeFilter builds a WHERE clause fragment and appends args for scope-aware
// chunk queries. It respects the three-scope model:
//
//	PROJECT scope: chunks where cc.project_id = projectID
//	AGENT scope:   chunks where cc.agent_id = agentID
//	ORG scope:     chunks where cc.org_id = orgID (project_id IS NULL)
//
// If scope is empty, all accessible chunks across all scopes are returned.
// Returns the clause fragment (starting with "AND") and the updated args slice.
func scopeFilter(scope, projectID, agentID, orgID string, args []interface{}) (string, []interface{}) {
	idx := func() int { return len(args) }

	switch scope {
	case "PROJECT":
		if projectID == "" {
			return "AND FALSE -- PROJECT scope requires project_id", args
		}
		args = append(args, projectID)
		return fmt.Sprintf("AND cc.scope = 'PROJECT' AND cc.project_id = $%d", idx()), args

	case "AGENT":
		if agentID == "" {
			return "AND FALSE -- AGENT scope requires agent_id", args
		}
		args = append(args, agentID)
		return fmt.Sprintf("AND cc.scope = 'AGENT' AND cc.agent_id = $%d", idx()), args

	case "ORG":
		if orgID == "" {
			return "AND FALSE -- ORG scope requires org_id", args
		}
		args = append(args, orgID)
		return fmt.Sprintf("AND cc.scope = 'ORG' AND cc.org_id = $%d", idx()), args

	default:
		// No scope filter — return all accessible chunks across all scopes.
		// Access rules: PROJECT chunks visible if project_id matches,
		// AGENT chunks visible if agent_id matches,
		// ORG chunks visible if org_id matches.
		sub := []string{}
		if projectID != "" {
			args = append(args, projectID)
			sub = append(sub, fmt.Sprintf("(cc.scope = 'PROJECT' AND cc.project_id = $%d)", len(args)))
		}
		if agentID != "" {
			args = append(args, agentID)
			sub = append(sub, fmt.Sprintf("(cc.scope = 'AGENT' AND cc.agent_id = $%d)", len(args)))
		}
		if orgID != "" {
			args = append(args, orgID)
			sub = append(sub, fmt.Sprintf("(cc.scope = 'ORG' AND cc.org_id = $%d)", len(args)))
		}
		if len(sub) == 0 {
			// No scope context at all — fall back to project_id on chunk (legacy)
			return "", args
		}
		clause := "AND (" + strings.Join(sub, " OR ") + ")"
		return clause, args
	}
}

// chunkTypeFilter returns a WHERE clause fragment for chunk_type filtering.
func chunkTypeFilter(chunkType string, args []interface{}) (string, []interface{}) {
	if chunkType == "" {
		return "", args
	}
	args = append(args, chunkType)
	return fmt.Sprintf("AND cc.chunk_type = $%d", len(args)), args
}

// alwaysInjectFilter returns a WHERE clause fragment for always_inject filtering.
func alwaysInjectFilter(onlyAlwaysInject bool, args []interface{}) (string, []interface{}) {
	if !onlyAlwaysInject {
		return "", args
	}
	return "AND cc.inject_audience IS NOT NULL", args
}

func applyTypeFilters(scope, chunkType string, tf models.AgentTypeFilterConfig) (effectiveScope, effectiveChunkType string) {
	effectiveScope = scope
	if scope == "" && len(tf.IncludeScopes) > 0 {
		effectiveScope = tf.IncludeScopes[0]
	}
	if effectiveScope != "" {
		effectiveScope = intersectOneOf(effectiveScope, tf.IncludeScopes)
	}
	if tf.OrgSearchRequiresExplicitScope && scope != "ORG" {
		effectiveScope = removeScope(effectiveScope, "ORG")
	}

	effectiveChunkType = chunkType
	if chunkType == "" && len(tf.IncludeChunkTypes) > 0 {
		effectiveChunkType = tf.IncludeChunkTypes[0]
	}
	if effectiveChunkType != "" {
		effectiveChunkType = intersectOneOf(effectiveChunkType, tf.IncludeChunkTypes)
	}

	return effectiveScope, effectiveChunkType
}

func intersectOneOf(value string, allowed []string) string {
	if len(allowed) == 0 {
		return value
	}
	for _, a := range allowed {
		if value == a {
			return value
		}
	}
	return ""
}

func removeScope(scope, toRemove string) string {
	if scope == toRemove {
		return ""
	}
	return scope
}

func excludeQueryKeyPrefixesClause(prefixes []string) string {
	if len(prefixes) == 0 {
		return ""
	}
	clauses := []string{}
	for _, p := range prefixes {
		clauses = append(clauses, fmt.Sprintf("cc.query_key NOT LIKE '%s%%'", p))
	}
	return " AND " + strings.Join(clauses, " AND ")
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

func scanChunkReadRow(row pgxScanner) (models.ContextChunk, int, error) {
	var chunk models.ContextChunk
	var version int
	var embeddingText *string
	var iaRaw []byte
	err := row.Scan(
		&chunk.ID,
		&chunk.ProjectID,
		&chunk.Scope,
		&chunk.AgentID,
		&chunk.OrgID,
		&iaRaw,
		&chunk.Visibility,
		&chunk.ChunkType,
		&chunk.QueryKey,
		&chunk.Title,
		&chunk.Content,
		&embeddingText,
		&chunk.SourceFile,
		&chunk.SourceLines,
		&chunk.Gotchas,
		&chunk.Related,
		&chunk.CreatedByAgent,
		&chunk.CreatedAt,
		&chunk.UpdatedAt,
		&version,
	)
	if err == nil && embeddingText != nil {
		err = chunk.Embedding.Parse(*embeddingText)
	}
	if err == nil && len(iaRaw) > 0 {
		var ia models.InjectAudience
		if err2 := json.Unmarshal(iaRaw, &ia); err2 == nil {
			chunk.InjectAudience = &ia
		}
	}
	return chunk, version, err
}

func scanChunkResultRow(row pgxScanner) (models.ContextChunk, int, float64, error) {
	var chunk models.ContextChunk
	var version int
	var score float64
	var embeddingText *string
	err := row.Scan(
		&chunk.ID,
		&chunk.ProjectID,
		&chunk.QueryKey,
		&chunk.Title,
		&chunk.Content,
		&embeddingText,
		&chunk.SourceFile,
		&chunk.SourceLines,
		&chunk.Gotchas,
		&chunk.Related,
		&chunk.CreatedByAgent,
		&chunk.CreatedAt,
		&chunk.UpdatedAt,
		&version,
		&score,
		&chunk.Visibility,
	)
	if err == nil && embeddingText != nil {
		err = chunk.Embedding.Parse(*embeddingText)
	}
	return chunk, version, score, err
}

// scanChunkSearchRow is like scanChunkResultRow but also scans the extra
// last_review_at column emitted by the SearchContext query.
func scanChunkSearchRow(row pgxScanner) (models.ContextChunk, int, float64, time.Time, error) {
	var chunk models.ContextChunk
	var version int
	var score float64
	var lastReviewAt time.Time
	var embeddingText *string
	err := row.Scan(
		&chunk.ID,
		&chunk.ProjectID,
		&chunk.QueryKey,
		&chunk.Title,
		&chunk.Content,
		&embeddingText,
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
		&chunk.Visibility,
	)
	if err == nil && embeddingText != nil {
		err = chunk.Embedding.Parse(*embeddingText)
	}
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
		Visibility:  chunk.Visibility,
		Score:       score,
		Version:     version,
		CreatedAt:   chunk.CreatedAt,
		UpdatedAt:   chunk.UpdatedAt,
	}
}

func readContextResultFromModel(chunk models.ContextChunk, version int) ChunkResult {
	result := chunkResultFromModel(chunk, version, 0)
	result.Scope = chunk.Scope
	result.AgentID = chunk.AgentID
	result.OrgID = chunk.OrgID
	result.InjectAudience = chunk.InjectAudience
	result.ChunkType = chunk.ChunkType
	result.Visibility = chunk.Visibility
	return result
}
