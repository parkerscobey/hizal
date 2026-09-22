package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/XferOps/hizal/internal/models"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- Input/Output types ----

type StartSessionInput struct {
	// AgentID is resolved from the API key — not required from the caller.
	AgentID       string  `json:"-"`
	ProjectID     *string `json:"project_id,omitempty"`
	LifecycleSlug *string `json:"lifecycle_slug,omitempty"` // defaults to "default"
	// ChunkDetail controls injected chunk payload: "full" (default, back-compat)
	// returns complete content; "summary" returns id/query_key/title/scope/type/size
	// only — pull full content for relevant chunks via read_context.
	ChunkDetail *string `json:"chunk_detail,omitempty"`
}

type StartSessionResult struct {
	SessionID      string          `json:"session_id"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Lifecycle      string          `json:"lifecycle"`
	RequiredSteps  []string        `json:"required_steps"`
	InjectedChunks []InjectedChunk `json:"injected_chunks,omitempty"`
	// ChunkSummaries carries summaries for chunks withheld from InjectedChunks:
	// every chunk in "summary" mode, or the over-budget dropped set in "full" mode.
	ChunkSummaries []InjectedChunkSummary `json:"chunk_summaries,omitempty"`
	// TotalContentSize is the summed len(content) chars of the full inject set.
	TotalContentSize int `json:"total_content_size,omitempty"`
	TruncatedCount   int `json:"truncated_count,omitempty"`
}

// InjectedChunkSummary is a content-free descriptor of an injectable chunk.
// Use read_context with the ID to pull full content.
type InjectedChunkSummary struct {
	ID        string `json:"id"`
	QueryKey  string `json:"query_key"`
	Title     string `json:"title"`
	Scope     string `json:"scope"`
	ChunkType string `json:"chunk_type"`
	Size      int    `json:"size"` // len(content) in chars
}

type InjectedChunk struct {
	ID        string    `json:"id"`
	QueryKey  string    `json:"query_key"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Scope     string    `json:"scope"`
	ChunkType string    `json:"chunk_type"`
	CreatedAt time.Time `json:"-"`
}

type ResumeSessionInput struct {
	SessionID string `json:"session_id"`
	// ChunkDetail mirrors StartSessionInput: "full" (default) or "summary".
	ChunkDetail *string `json:"chunk_detail,omitempty"`
}

type ResumeSessionResult struct {
	SessionID      string          `json:"session_id"`
	ExpiresAt      time.Time       `json:"expires_at"`
	FocusTask      *string         `json:"focus_task,omitempty"`
	ChunksWritten  int             `json:"chunks_written"`
	ResumeCount    int             `json:"resume_count"`
	InjectedChunks []InjectedChunk `json:"injected_chunks,omitempty"`
	// ChunkSummaries mirrors StartSessionResult: every chunk in "summary"
	// mode, or the over-budget dropped set in "full" mode.
	ChunkSummaries []InjectedChunkSummary `json:"chunk_summaries,omitempty"`
	// TotalContentSize is the summed len(content) chars of the full inject set.
	TotalContentSize int `json:"total_content_size,omitempty"`
	TruncatedCount   int `json:"truncated_count,omitempty"`
}

type RegisterFocusInput struct {
	SessionID string   `json:"session_id"`
	Task      string   `json:"task"`
	Tags      []string `json:"tags,omitempty"`
}

type RegisterFocusResult struct {
	SessionID string `json:"session_id"`
	FocusTask string `json:"focus_task"`
	// FocusInjectedChunks is the total number of focus-tag-matched chunks in
	// the session inject set after this call (newly added + already present).
	// Zero now truly means nothing matched — previously it counted only newly
	// added chunks, so already-injected matches misleadingly reported 0.
	FocusInjectedChunks int `json:"focus_injected_chunks"`
	// FocusNewChunks counts chunks newly added to the inject set by this call.
	FocusNewChunks int `json:"focus_new_chunks,omitempty"`
	// FocusChunks describes every focus-tag-matched chunk (newly added first),
	// so the caller gets usable context without an extra search round-trip.
	FocusChunks []FocusMatchedChunk `json:"focus_chunks,omitempty"`
}

// FocusMatchedChunk is a content-free descriptor of a focus-tag-matched chunk.
// Pull full content via read_context.
type FocusMatchedChunk struct {
	ID        string `json:"id"`
	QueryKey  string `json:"query_key"`
	Title     string `json:"title"`
	Scope     string `json:"scope"`
	ChunkType string `json:"chunk_type"`
}

type EndSessionInput struct {
	SessionID string `json:"session_id"`
}

type EndSessionResult struct {
	SessionID     string                `json:"session_id"`
	ChunksWritten int                   `json:"chunks_written"`
	ChunksRead    int                   `json:"chunks_read"`
	WriteChunks   []SessionChunkSummary `json:"write_chunks"` // chunks written during session for consolidation review
}

type SessionChunkSummary struct {
	ID       string `json:"id"`
	QueryKey string `json:"query_key"`
	Title    string `json:"title"`
	Scope    string `json:"scope"`
}

// ActiveSessionError is returned by StartSession when the agent already has an
// active session. It carries the existing session's ID and expiry so the caller
// can immediately resume or inspect rather than guessing what went wrong.
type ActiveSessionError struct {
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Message   string    `json:"message"`
}

func (e *ActiveSessionError) Error() string {
	return fmt.Sprintf(
		"agent already has an active session (id=%s, expires_at=%s) — call resume_session to extend TTL or get_active_session to inspect",
		e.SessionID, e.ExpiresAt.Format(time.RFC3339),
	)
}

// ValidationError is returned by tools when validation fails and the error
// message should be surfaced to the caller (agent or developer) rather than
// being wrapped in a generic "internal error".
type ValidationError struct {
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return e.Message
}

// ---- Helpers ----

// resolveLifecycle fetches the lifecycle for the given slug (org-specific first,
// then global preset). Falls back to the global "default" preset if not found.
func (t *Tools) resolveLifecycle(ctx context.Context, orgID string, slug string) (*models.SessionLifecycle, error) {
	if slug == "" {
		slug = "default"
	}

	// Try org-specific first, then global preset.
	row := t.pool.QueryRow(ctx, `
		SELECT id, org_id, name, slug, is_default, description, config, created_at, updated_at
		FROM session_lifecycles
		WHERE slug = $1 AND (org_id = $2 OR org_id IS NULL)
		ORDER BY org_id NULLS LAST
		LIMIT 1
	`, slug, orgID)

	lc := &models.SessionLifecycle{}
	err := row.Scan(&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &lc.Description, &lc.Config, &lc.CreatedAt, &lc.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("lifecycle %q not found", slug)
		}
		return nil, fmt.Errorf("resolveLifecycle: %w", err)
	}
	return lc, nil
}

func parseLifecycleConfig(lc *models.SessionLifecycle) (models.SessionLifecycleConfig, error) {
	var cfg models.SessionLifecycleConfig
	if err := json.Unmarshal(lc.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("parseLifecycleConfig: %w", err)
	}
	if cfg.TTLHours == 0 {
		cfg.TTLHours = 8
	}
	if len(cfg.InjectScopes) == 0 {
		cfg.InjectScopes = []string{"AGENT", "PROJECT", "ORG"}
	}
	return cfg, nil
}

func intersectScopes(a, b []string) []string {
	set := make(map[string]bool)
	for _, s := range b {
		set[s] = true
	}
	var result []string
	for _, s := range a {
		if set[s] {
			result = append(result, s)
		}
	}
	return result
}

// normalizeChunkDetail resolves the chunk_detail param: "" / nil → "full"
// (back-compat). Anything other than "full" | "summary" is an error.
func normalizeChunkDetail(raw *string) (string, error) {
	if raw == nil || *raw == "" {
		return "full", nil
	}
	switch *raw {
	case "full", "summary":
		return *raw, nil
	default:
		return "", fmt.Errorf("chunk_detail must be \"full\" or \"summary\", got %q", *raw)
	}
}

// summarizeInjectedChunks builds content-free descriptors plus the summed
// content size of the set, so agents can decide what to pull via read_context.
func summarizeInjectedChunks(chunks []InjectedChunk) ([]InjectedChunkSummary, int) {
	summaries := make([]InjectedChunkSummary, 0, len(chunks))
	total := 0
	for _, c := range chunks {
		size := len(c.Content)
		total += size
		summaries = append(summaries, InjectedChunkSummary{
			ID:        c.ID,
			QueryKey:  c.QueryKey,
			Title:     c.Title,
			Scope:     c.Scope,
			ChunkType: c.ChunkType,
			Size:      size,
		})
	}
	return summaries, total
}

// totalInjectedSize sums len(content) chars across chunk sets.
func totalInjectedSize(sets ...[]InjectedChunk) int {
	total := 0
	for _, set := range sets {
		for _, c := range set {
			total += len(c.Content)
		}
	}
	return total
}

// validateStartProject ensures an explicitly passed project_id belongs to the
// org (and to the agent, for agent keys) before a session row is created.
// Returns a recovery-hinting error naming list_projects.
func (t *Tools) validateStartProject(ctx context.Context, orgID, agentID string, projectID *string) error {
	if projectID == nil || *projectID == "" {
		return nil
	}
	var foundOrgID string
	var name string
	err := t.pool.QueryRow(ctx, `
		SELECT org_id, name FROM projects WHERE id = $1
	`, *projectID).Scan(&foundOrgID, &name)
	if err != nil || foundOrgID != orgID {
		return fmt.Errorf("project_id %q is not accessible for this API key — call list_projects to see available project IDs, then retry start_session with a valid project_id (or omit project_id for an agent/org-only session)", *projectID)
	}
	if agentID != "" {
		var hasAccess bool
		err := t.pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM agent_projects WHERE agent_id = $1 AND project_id = $2)
		`, agentID, *projectID).Scan(&hasAccess)
		if err != nil || !hasAccess {
			return fmt.Errorf("project_id %q is not accessible for this agent — call list_projects to see available project IDs, then retry start_session with a valid project_id (or omit project_id for an agent/org-only session)", *projectID)
		}
	}
	return nil
}

func (t *Tools) resolveAgentInjectFilters(ctx context.Context, agentID string) models.AgentTypeFilterConfig {
	var rawFilters []byte
	err := t.pool.QueryRow(ctx, `
		SELECT COALESCE(at.inject_filters, '{}')
		FROM agents a
		LEFT JOIN agent_types at ON at.id = a.type_id
		WHERE a.id = $1
	`, agentID).Scan(&rawFilters)
	if err != nil {
		return models.AgentTypeFilterConfig{}
	}
	var filters models.AgentTypeFilterConfig
	if err := json.Unmarshal(rawFilters, &filters); err != nil {
		return models.AgentTypeFilterConfig{}
	}
	return filters
}

func (t *Tools) resolveAgentType(ctx context.Context, agentID string) string {
	var typeSlug *string
	err := t.pool.QueryRow(ctx, `
		SELECT at.slug
		FROM agents a
		LEFT JOIN agent_types at ON at.id = a.type_id
		WHERE a.id = $1
	`, agentID).Scan(&typeSlug)
	if err != nil || typeSlug == nil {
		return ""
	}
	return *typeSlug
}

func (t *Tools) resolveAgentTags(ctx context.Context, agentID string) []string {
	var tags []string
	err := t.pool.QueryRow(ctx, `
		SELECT tags FROM agents WHERE id = $1
	`, agentID).Scan(&tags)
	if err != nil {
		return []string{}
	}
	return tags
}

func (t *Tools) fetchInjectAudienceCandidates(
	ctx context.Context,
	agentID string,
	agentType string,
	agentTags []string,
	lifecycleType *string,
	projectID *string,
	orgID string,
	scopes []string,
	includeChunkTypes []string,
	excludeChunkTypes []string,
	excludeQueryKeys []string,
	maxInjectTokens int,
	focusTags []string,
) ([]InjectedChunk, []InjectedChunk, error) {
	args := []any{agentID, orgID, scopes}
	projectFilter := "AND (cc.scope != 'PROJECT')"
	if projectID != nil {
		args = append(args, *projectID)
		projectFilter = fmt.Sprintf("AND (cc.scope != 'PROJECT' OR cc.project_id = $%d)", len(args))
	}

	chunkTypeFilter := ""
	if len(includeChunkTypes) > 0 {
		args = append(args, includeChunkTypes)
		chunkTypeFilter = fmt.Sprintf(" AND cc.chunk_type = ANY($%d)", len(args))
	}

	queryKeyFilter := ""
	if len(excludeQueryKeys) > 0 {
		args = append(args, excludeQueryKeys)
		queryKeyFilter = fmt.Sprintf(" AND cc.query_key != ALL($%d)", len(args))
	}

	query := fmt.Sprintf(`
		SELECT cc.id, cc.query_key, cc.title, cc.content, cc.scope, cc.chunk_type, cc.inject_audience, cc.created_at
		FROM context_chunks cc
		WHERE cc.inject_audience IS NOT NULL
		  AND cc.scope = ANY($3)
		  %s%s%s
		  AND (
		    (cc.scope = 'AGENT' AND cc.agent_id = $1)
		    OR (cc.scope = 'ORG' AND cc.project_id IS NULL AND cc.org_id = $2)
		    OR (cc.scope = 'PROJECT' AND cc.project_id IS NOT NULL)
		  )
		ORDER BY
		  CASE cc.scope WHEN 'AGENT' THEN 1 WHEN 'ORG' THEN 2 WHEN 'PROJECT' THEN 3 END,
		  cc.updated_at DESC
	`, projectFilter, chunkTypeFilter, queryKeyFilter)

	rows, err := t.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("fetchInjectAudienceCandidates: %w", err)
	}
	defer rows.Close()

	var candidates []struct {
		InjectedChunk
		iaRaw []byte
	}
	for rows.Next() {
		var c InjectedChunk
		var rawContent []byte
		var iaRaw []byte
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &rawContent, &c.Scope, &c.ChunkType, &iaRaw, &c.CreatedAt); err != nil {
			return nil, nil, err
		}
		c.Content = decodeContent(rawContent)
		candidates = append(candidates, struct {
			InjectedChunk
			iaRaw []byte
		}{InjectedChunk: c, iaRaw: iaRaw})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	type candidateWithRule struct {
		InjectedChunk
		ia      models.InjectAudience
		rule    models.InjectAudienceRule
		ruleKey string
	}

	lifecycleStr := ""
	if lifecycleType != nil {
		lifecycleStr = *lifecycleType
	}
	projectIDStr := ""
	if projectID != nil {
		projectIDStr = *projectID
	}

	// Match each candidate against individual rules (not just MatchesSession)
	// so we know exactly which rule(s) fired for each chunk. This is needed
	// because the "latest" predicate caps per-rule, not globally.
	//
	// Design note: the latest grouping works cleanly when chunks share the same
	// single-rule inject_audience. With multi-rule inject_audiences at different
	// indices, a chunk may appear in multiple rule buckets — this is intentional,
	// as each rule independently controls its own latest cap.
	var matchedByRule []candidateWithRule
	for _, cand := range candidates {
		if len(cand.iaRaw) == 0 {
			continue
		}
		var ia models.InjectAudience
		if err := json.Unmarshal(cand.iaRaw, &ia); err != nil {
			continue
		}
		for _, rule := range ia.Rules {
			if rule.Matches(agentID, agentType, lifecycleStr, orgID, projectIDStr, agentTags, focusTags) {
				ruleKey := fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v|%d",
					rule.All, rule.AgentIDs, rule.AgentTypes, rule.LifecycleTypes,
					rule.AgentTags, rule.OrgIDs, rule.ProjectIDs, rule.Latest)
				matchedByRule = append(matchedByRule, candidateWithRule{
					InjectedChunk: cand.InjectedChunk,
					ia:            ia,
					rule:          rule,
					ruleKey:       ruleKey,
				})
			}
		}
	}

	var chunks []InjectedChunk
	if len(matchedByRule) == 0 {
		chunks = nil
	} else {
		ruleMatched := make(map[string][]InjectedChunk)
		ruleLatest := make(map[string]int)
		for _, m := range matchedByRule {
			ruleMatched[m.ruleKey] = append(ruleMatched[m.ruleKey], m.InjectedChunk)
			if ruleLatest[m.ruleKey] == 0 {
				ruleLatest[m.ruleKey] = m.rule.Latest
			}
		}

		ruleKeys := make([]string, 0, len(ruleMatched))
		for key := range ruleMatched {
			ruleKeys = append(ruleKeys, key)
		}
		sort.Strings(ruleKeys)

		var allMatched []InjectedChunk
		seen := make(map[string]bool)
		for _, ruleKey := range ruleKeys {
			ruleChunks := ruleMatched[ruleKey]
			latest := ruleLatest[ruleKey]
			if latest > 0 && len(ruleChunks) > latest {
				sort.Slice(ruleChunks, func(i, j int) bool {
					return ruleChunks[i].CreatedAt.After(ruleChunks[j].CreatedAt)
				})
				ruleChunks = ruleChunks[:latest]
			}
			for _, c := range ruleChunks {
				if !seen[c.ID] {
					seen[c.ID] = true
					allMatched = append(allMatched, c)
				}
			}
		}
		chunks = allMatched
	}

	var dropped []InjectedChunk
	if maxInjectTokens > 0 {
		var kept []InjectedChunk
		runningTokens := 0
		for _, chunk := range chunks {
			estTokens := len(chunk.Content) / 4
			if runningTokens+estTokens <= maxInjectTokens {
				kept = append(kept, chunk)
				runningTokens += estTokens
			} else {
				dropped = append(dropped, chunk)
			}
		}
		chunks = kept
	}

	return chunks, dropped, nil
}

func (t *Tools) cacheInjectSet(ctx context.Context, sessionID string, chunks []InjectedChunk) {
	if len(chunks) == 0 {
		return
	}
	chunkIDs := make([]string, len(chunks))
	for i, c := range chunks {
		chunkIDs[i] = c.ID
	}
	injectSetJSON, _ := json.Marshal(chunkIDs)
	_, _ = t.pool.Exec(ctx, `
		UPDATE sessions SET inject_set = $1, updated_at = NOW() WHERE id = $2
	`, injectSetJSON, sessionID)
}

func (t *Tools) incrementSessionActivity(agentID, orgID string, isWrite bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	col := "chunks_read"
	if isWrite {
		col = "chunks_written"
	}

	_, _ = t.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE sessions
		SET %s = %s + 1, updated_at = NOW()
		WHERE org_id = $1
		  AND status = 'active'
		  AND agent_id = (SELECT id FROM agents WHERE id = $2 LIMIT 1)
	`, col, col), orgID, agentID)
}

// ---- Tool Implementations ----

// StartSession begins a new session for an agent.
// Returns the session ID and all matching chunks for the agent's context window.
// Fails if the agent already has an active session (use ResumeSession instead).
func (t *Tools) StartSession(ctx context.Context, orgID string, agentID string, in StartSessionInput) (*StartSessionResult, error) {
	if agentID == "" {
		return nil, fmt.Errorf("could not resolve agent from API key — ensure you are using an agent API key, not an org key")
	}
	in.AgentID = agentID

	lifecycleSlug := "default"
	if in.LifecycleSlug != nil && *in.LifecycleSlug != "" {
		lifecycleSlug = *in.LifecycleSlug
	}

	detail, err := normalizeChunkDetail(in.ChunkDetail)
	if err != nil {
		return nil, err
	}

	if err := t.validateStartProject(ctx, orgID, in.AgentID, in.ProjectID); err != nil {
		return nil, err
	}

	lc, err := t.resolveLifecycle(ctx, orgID, lifecycleSlug)
	if err != nil {
		return nil, err
	}
	lcCfg, err := parseLifecycleConfig(lc)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(time.Duration(lcCfg.TTLHours) * time.Hour)

	var sessionID string
	err = t.pool.QueryRow(ctx, `
		INSERT INTO sessions (agent_id, project_id, org_id, lifecycle_id, status, expires_at)
		VALUES ($1, $2, $3, $4, 'active', $5)
		RETURNING id
	`, in.AgentID, in.ProjectID, orgID, lc.ID, expiresAt).Scan(&sessionID)
	if err != nil {
		// Unique constraint violation means the agent already has an active session.
		// Return structured info so the caller can resume or inspect instead of
		// seeing an opaque internal error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			var existingID string
			var existingExpiry time.Time
			qerr := t.pool.QueryRow(ctx, `
				SELECT id, expires_at FROM sessions
				WHERE agent_id = $1 AND status = 'active'
				LIMIT 1
			`, in.AgentID).Scan(&existingID, &existingExpiry)
			if qerr == nil {
				return nil, &ActiveSessionError{
					SessionID: existingID,
					ExpiresAt: existingExpiry,
					Message:   "agent already has an active session — call resume_session to extend TTL or get_active_session to retrieve session info",
				}
			}
		}
		return nil, fmt.Errorf("start_session: insert failed: %w", err)
	}

	typeFilters := t.resolveAgentInjectFilters(ctx, agentID)
	scopes := lcCfg.InjectScopes
	if len(typeFilters.IncludeScopes) > 0 {
		scopes = intersectScopes(scopes, typeFilters.IncludeScopes)
	}

	agentTags := t.resolveAgentTags(ctx, in.AgentID)
	chunks, dropped, err := t.fetchInjectAudienceCandidates(
		ctx, in.AgentID, t.resolveAgentType(ctx, in.AgentID), agentTags, &lifecycleSlug, in.ProjectID, orgID, scopes,
		typeFilters.IncludeChunkTypes,
		typeFilters.ExcludeChunkTypes,
		typeFilters.ExcludeQueryKeys,
		typeFilters.MaxInjectTokens,
		nil,
	)
	if err != nil {
		return nil, err
	}

	t.cacheInjectSet(ctx, sessionID, chunks)

	result := &StartSessionResult{
		SessionID:        sessionID,
		ExpiresAt:        expiresAt,
		Lifecycle:        lc.Slug,
		RequiredSteps:    lcCfg.RequiredSteps,
		TotalContentSize: totalInjectedSize(chunks, dropped),
	}
	if detail == "summary" {
		result.ChunkSummaries, _ = summarizeInjectedChunks(append(chunks, dropped...))
	} else {
		result.InjectedChunks = chunks
		if len(dropped) > 0 {
			result.ChunkSummaries, _ = summarizeInjectedChunks(dropped)
			result.TruncatedCount = len(dropped)
		}
	}
	return result, nil
}

// ResumeSession extends an existing active session's TTL and re-injects
// matching chunks fresh. Use after a break or when resuming across
// tool calls. Extends TTL by the lifecycle's ttl_hours from now.
func (t *Tools) ResumeSession(ctx context.Context, orgID string, in ResumeSessionInput) (*ResumeSessionResult, error) {
	if in.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	detail, err := normalizeChunkDetail(in.ChunkDetail)
	if err != nil {
		return nil, err
	}

	// Fetch session + lifecycle in one query.
	var sess models.Session
	var lcConfig []byte
	var lcSlug *string
	err = t.pool.QueryRow(ctx, `
		SELECT s.id, s.agent_id, s.project_id, s.org_id, s.lifecycle_id,
		       s.status, s.focus_task, s.focus_tags, s.chunks_written, s.chunks_read,
		       s.consolidation_done, s.resume_count, s.expires_at,
		       s.started_at, s.ended_at, s.created_at, s.updated_at,
		       COALESCE(sl.config, '{"ttl_hours":8,"inject_scopes":["AGENT","PROJECT","ORG"]}'::jsonb) as lc_config,
		       sl.slug as lc_slug
		FROM sessions s
		LEFT JOIN session_lifecycles sl ON sl.id = s.lifecycle_id
		WHERE s.id = $1 AND s.org_id = $2
	`, in.SessionID, orgID).Scan(
		&sess.ID, &sess.AgentID, &sess.ProjectID, &sess.OrgID, &sess.LifecycleID,
		&sess.Status, &sess.FocusTask, &sess.FocusTags, &sess.ChunksWritten, &sess.ChunksRead,
		&sess.ConsolidationDone, &sess.ResumeCount, &sess.ExpiresAt,
		&sess.StartedAt, &sess.EndedAt, &sess.CreatedAt, &sess.UpdatedAt,
		&lcConfig, &lcSlug,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("session not found")
		}
		return nil, fmt.Errorf("ResumeSession fetch: %w", err)
	}
	if sess.Status != "active" {
		return nil, fmt.Errorf("session is %s — cannot resume", sess.Status)
	}

	var cfg models.SessionLifecycleConfig
	if err := json.Unmarshal(lcConfig, &cfg); err != nil {
		cfg.TTLHours = 8
		cfg.InjectScopes = []string{"AGENT", "PROJECT", "ORG"}
	}
	if cfg.TTLHours == 0 {
		cfg.TTLHours = 8
	}
	if len(cfg.InjectScopes) == 0 {
		cfg.InjectScopes = []string{"AGENT", "PROJECT", "ORG"}
	}

	newExpiry := time.Now().Add(time.Duration(cfg.TTLHours) * time.Hour)

	_, err = t.pool.Exec(ctx, `
		UPDATE sessions
		SET expires_at = $1, resume_count = resume_count + 1, updated_at = NOW()
		WHERE id = $2
	`, newExpiry, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("ResumeSession update: %w", err)
	}

	typeFilters := t.resolveAgentInjectFilters(ctx, sess.AgentID)
	scopes := cfg.InjectScopes
	if len(typeFilters.IncludeScopes) > 0 {
		scopes = intersectScopes(scopes, typeFilters.IncludeScopes)
	}

	agentTags := t.resolveAgentTags(ctx, sess.AgentID)
	chunks, dropped, err := t.fetchInjectAudienceCandidates(
		ctx, sess.AgentID, t.resolveAgentType(ctx, sess.AgentID), agentTags, lcSlug, sess.ProjectID, orgID, scopes,
		typeFilters.IncludeChunkTypes,
		typeFilters.ExcludeChunkTypes,
		typeFilters.ExcludeQueryKeys,
		0,
		sess.FocusTags,
	)
	if err != nil {
		return nil, err
	}

	t.cacheInjectSet(ctx, sess.ID, chunks)

	result := &ResumeSessionResult{
		SessionID:        sess.ID,
		ExpiresAt:        newExpiry,
		FocusTask:        sess.FocusTask,
		ChunksWritten:    sess.ChunksWritten,
		ResumeCount:      sess.ResumeCount + 1,
		TotalContentSize: totalInjectedSize(chunks, dropped),
	}
	if detail == "summary" {
		result.ChunkSummaries, _ = summarizeInjectedChunks(append(chunks, dropped...))
	} else {
		result.InjectedChunks = chunks
		if len(dropped) > 0 {
			result.ChunkSummaries, _ = summarizeInjectedChunks(dropped)
			result.TruncatedCount = len(dropped)
		}
	}
	return result, nil
}

// RegisterFocus records what task the agent is currently working on within a session.
// Stored on the session row. Required if the lifecycle config has "register_focus"
// in required_steps.
func (t *Tools) RegisterFocus(ctx context.Context, orgID string, in RegisterFocusInput) (*RegisterFocusResult, error) {
	if in.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if in.Task == "" {
		return nil, fmt.Errorf("task is required")
	}

	// Default to empty slice if tags not provided (column is NOT NULL)
	if in.Tags == nil {
		in.Tags = []string{}
	}

	var sessionID string
	err := t.pool.QueryRow(ctx, `
		UPDATE sessions
		SET focus_task = $1, focus_tags = $4, updated_at = NOW()
		WHERE id = $2 AND org_id = $3 AND status = 'active'
		RETURNING id
	`, in.Task, in.SessionID, orgID, in.Tags).Scan(&sessionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("active session not found")
		}
		return nil, fmt.Errorf("RegisterFocus: %w", err)
	}

	focusResult := &RegisterFocusResult{
		SessionID: sessionID,
		FocusTask: in.Task,
	}
	upd, err := t.updateFocusInjectSet(ctx, orgID, in.SessionID, in.Tags)
	if err != nil {
		log.Printf("focus inject set update failed: %v", err)
	} else {
		focusResult.FocusInjectedChunks = len(upd.Matched)
		focusResult.FocusNewChunks = len(upd.NewIDs)
		focusResult.FocusChunks = upd.Matched
	}

	return focusResult, nil
}

// focusInjectUpdate is the outcome of updateFocusInjectSet: the IDs newly
// added to the session inject set plus descriptors for every focus-matched
// chunk (newly added first, then already-present).
type focusInjectUpdate struct {
	NewIDs  []string
	Matched []FocusMatchedChunk
}

// focusCandidate is one injectable chunk row examined for focus-tag matches.
type focusCandidate struct {
	ID        string
	QueryKey  string
	Title     string
	Scope     string
	ChunkType string
	IARaw     []byte
}

// matchFocusTags reports whether any rule in the chunk's inject_audience
// targets one of the session's focus tags. Malformed payloads never match.
func matchFocusTags(iaRaw []byte, focusTags []string) bool {
	if len(focusTags) == 0 || len(iaRaw) == 0 {
		return false
	}
	var ia models.InjectAudience
	if err := json.Unmarshal(iaRaw, &ia); err != nil {
		return false
	}
	for _, rule := range ia.Rules {
		if len(rule.FocusTags) > 0 && models.AnyOverlap(rule.FocusTags, focusTags) {
			return true
		}
	}
	return false
}

// partitionFocusMatches splits focus-matching candidates into newly-added vs
// already-in-set IDs, returning the update payload. Pure (no DB) for testing.
func partitionFocusMatches(currentIDs []string, candidates []focusCandidate, focusTags []string) focusInjectUpdate {
	alreadyIn := make(map[string]bool, len(currentIDs))
	for _, id := range currentIDs {
		alreadyIn[id] = true
	}
	upd := focusInjectUpdate{}
	for _, c := range candidates {
		if !matchFocusTags(c.IARaw, focusTags) {
			continue
		}
		matched := FocusMatchedChunk{
			ID:        c.ID,
			QueryKey:  c.QueryKey,
			Title:     c.Title,
			Scope:     c.Scope,
			ChunkType: c.ChunkType,
		}
		upd.Matched = append(upd.Matched, matched)
		if !alreadyIn[c.ID] {
			upd.NewIDs = append(upd.NewIDs, c.ID)
			alreadyIn[c.ID] = true
		}
	}
	// Newly added first: stable, useful ordering for callers.
	if len(upd.NewIDs) > 0 {
		isNew := make(map[string]bool, len(upd.NewIDs))
		for _, id := range upd.NewIDs {
			isNew[id] = true
		}
		ordered := upd.Matched[:0]
		for _, m := range upd.Matched {
			if isNew[m.ID] {
				ordered = append(ordered, m)
			}
		}
		for _, m := range upd.Matched {
			if !isNew[m.ID] {
				ordered = append(ordered, m)
			}
		}
		upd.Matched = ordered
	}
	return upd
}

// updateFocusInjectSet finds chunks with focus_tags conditions that match
// the new focus tags, adds the missing ones to the session's inject_set, and
// returns descriptors for every match (new + already present) so callers get
// usable context without another round-trip.
func (t *Tools) updateFocusInjectSet(ctx context.Context, orgID, sessionID string, focusTags []string) (focusInjectUpdate, error) {
	if len(focusTags) == 0 {
		return focusInjectUpdate{}, nil
	}

	var injectSetJSON []byte
	err := t.pool.QueryRow(ctx,
		`SELECT inject_set FROM sessions WHERE id = $1 AND org_id = $2`,
		sessionID, orgID,
	).Scan(&injectSetJSON)
	if err != nil {
		return focusInjectUpdate{}, err
	}

	var currentIDs []string
	if len(injectSetJSON) > 0 {
		_ = json.Unmarshal(injectSetJSON, &currentIDs)
	}

	rows, err := t.pool.Query(ctx, `
		SELECT id, query_key, title, scope, chunk_type, inject_audience
		FROM context_chunks
		WHERE org_id = $1
		  AND inject_audience IS NOT NULL
	`, orgID)
	if err != nil {
		return focusInjectUpdate{}, err
	}
	defer rows.Close()

	var candidates []focusCandidate
	for rows.Next() {
		var c focusCandidate
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &c.Scope, &c.ChunkType, &c.IARaw); err != nil {
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return focusInjectUpdate{}, err
	}

	upd := partitionFocusMatches(currentIDs, candidates, focusTags)

	if len(upd.NewIDs) == 0 {
		return upd, nil
	}

	allIDs := append(currentIDs, upd.NewIDs...)
	allIDsJSON, _ := json.Marshal(allIDs)
	_, err = t.pool.Exec(ctx,
		`UPDATE sessions SET inject_set = $1 WHERE id = $2 AND org_id = $3`,
		allIDsJSON, sessionID, orgID,
	)
	if err != nil {
		return focusInjectUpdate{}, err
	}

	return upd, nil
}

// EndSession closes the session and returns the chunks written during it
// so the agent can perform KEEP / PROMOTE / DISCARD consolidation.
func (t *Tools) EndSession(ctx context.Context, orgID string, in EndSessionInput) (*EndSessionResult, error) {
	if in.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	var sess models.Session
	err := t.pool.QueryRow(ctx, `
		UPDATE sessions
		SET status = 'ended', ended_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND status = 'active'
		RETURNING id, chunks_written, chunks_read
	`, in.SessionID, orgID).Scan(&sess.ID, &sess.ChunksWritten, &sess.ChunksRead)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("active session not found")
		}
		return nil, fmt.Errorf("EndSession: %w", err)
	}

	// Return chunks written during this session whose type has consolidation_behavior=SURFACE.
	rows, err := t.pool.Query(ctx, `
		SELECT cc.id, cc.query_key, cc.title, cc.scope
		FROM context_chunks cc
		JOIN chunk_types ct ON ct.slug = cc.chunk_type
		WHERE cc.agent_id = (SELECT agent_id FROM sessions WHERE id = $1)
		  AND (ct.org_id IS NULL OR ct.org_id = (
		      SELECT org_id FROM sessions WHERE id = $1
		  ))
		  AND ct.consolidation_behavior = 'SURFACE'
		  AND cc.created_at >= (SELECT started_at FROM sessions WHERE id = $1)
		ORDER BY cc.created_at ASC
	`, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("EndSession fetch chunks: %w", err)
	}
	defer rows.Close()

	var writeChunks []SessionChunkSummary
	for rows.Next() {
		var c SessionChunkSummary
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &c.Scope); err != nil {
			return nil, err
		}
		writeChunks = append(writeChunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &EndSessionResult{
		SessionID:     sess.ID,
		ChunksWritten: sess.ChunksWritten,
		ChunksRead:    sess.ChunksRead,
		WriteChunks:   writeChunks,
	}, nil
}

// GetActiveSessionResult is returned by GetActiveSession.
type GetActiveSessionResult struct {
	SessionID     *string  `json:"session_id"`
	Status        string   `json:"status"` // "active" | "none"
	LifecycleSlug *string  `json:"lifecycle_slug,omitempty"`
	FocusTask     *string  `json:"focus_task,omitempty"`
	ExpiresAt     *string  `json:"expires_at,omitempty"`
	ChunksWritten int      `json:"chunks_written"`
	ResumeCount   int      `json:"resume_count"`
	InjectSet     []string `json:"inject_set,omitempty"`
	Message       string   `json:"message"`
}

// GetActiveSession returns the calling agent's current active session, derived
// from the API key. No input required. Returns status="none" if no active session exists.
func (t *Tools) GetActiveSession(ctx context.Context, agentID string) (*GetActiveSessionResult, error) {
	if agentID == "" {
		return nil, fmt.Errorf("could not resolve agent from API key — ensure you are using an agent API key, not an org key")
	}

	var (
		sessionID      string
		lifecycleSlug  string
		focusTask      *string
		expiresAt      time.Time // TIMESTAMPTZ — scan to time.Time then format
		chunksWritten  int
		resumeCount    int
		injectSetBytes []byte // inject_set is JSONB — scan to []byte then unmarshal
	)

	// LEFT JOIN so sessions with a NULL or deleted lifecycle_id still resolve.
	err := t.pool.QueryRow(ctx, `
		SELECT s.id, COALESCE(sl.slug, 'default'), s.focus_task, s.expires_at, s.chunks_written, s.resume_count, s.inject_set
		FROM sessions s
		LEFT JOIN session_lifecycles sl ON sl.id = s.lifecycle_id
		WHERE s.agent_id = $1 AND s.status = 'active'
		LIMIT 1
	`, agentID).Scan(&sessionID, &lifecycleSlug, &focusTask, &expiresAt, &chunksWritten, &resumeCount, &injectSetBytes)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Normal case: no active session exists.
			return &GetActiveSessionResult{
				Status:  "none",
				Message: "no active session — call start_session to begin one",
			}, nil
		}
		// Propagate unexpected errors rather than silently masking them as "none".
		return nil, fmt.Errorf("GetActiveSession query: %w", err)
	}

	var injectSet []string
	if len(injectSetBytes) > 0 {
		_ = json.Unmarshal(injectSetBytes, &injectSet)
	}
	expiresAtStr := expiresAt.Format(time.RFC3339)

	return &GetActiveSessionResult{
		SessionID:     &sessionID,
		Status:        "active",
		LifecycleSlug: &lifecycleSlug,
		FocusTask:     focusTask,
		ExpiresAt:     &expiresAtStr,
		ChunksWritten: chunksWritten,
		ResumeCount:   resumeCount,
		InjectSet:     injectSet,
		Message:       "active session found — use this session_id to continue; call resume_session to extend TTL if needed",
	}, nil
}
