package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/XferOps/winnow/internal/models"
	"github.com/jackc/pgx/v5"
)

// ---- Input/Output types ----

type StartSessionInput struct {
	// AgentID is resolved from the API key — not required from the caller.
	AgentID       string  `json:"-"`
	ProjectID     *string `json:"project_id,omitempty"`
	LifecycleSlug *string `json:"lifecycle_slug,omitempty"` // defaults to "default"
}

type StartSessionResult struct {
	SessionID      string          `json:"session_id"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Lifecycle      string          `json:"lifecycle"`
	RequiredSteps  []string        `json:"required_steps"`
	InjectedChunks []InjectedChunk `json:"injected_chunks"`
	TruncatedCount int             `json:"truncated_count,omitempty"`
}

type InjectedChunk struct {
	ID        string `json:"id"`
	QueryKey  string `json:"query_key"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Scope     string `json:"scope"`
	ChunkType string `json:"chunk_type"`
}

type ResumeSessionInput struct {
	SessionID string `json:"session_id"`
}

type ResumeSessionResult struct {
	SessionID      string          `json:"session_id"`
	ExpiresAt      time.Time       `json:"expires_at"`
	FocusTask      *string         `json:"focus_task,omitempty"`
	ChunksWritten  int             `json:"chunks_written"`
	ResumeCount    int             `json:"resume_count"`
	InjectedChunks []InjectedChunk `json:"injected_chunks"`
}

type RegisterFocusInput struct {
	SessionID string `json:"session_id"`
	Task      string `json:"task"`
}

type RegisterFocusResult struct {
	SessionID string `json:"session_id"`
	FocusTask string `json:"focus_task"`
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
) ([]InjectedChunk, int, error) {
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
		SELECT cc.id, cc.query_key, cc.title, cc.content, cc.scope, cc.chunk_type, cc.inject_audience
		FROM context_chunks cc
		WHERE cc.inject_audience IS NOT NULL
		  AND cc.scope = ANY($3)
		  %s%s%s
		  AND (
		    (cc.scope = 'AGENT' AND cc.agent_id = $1)
		    OR (cc.scope = 'ORG' AND cc.project_id IS NULL AND EXISTS (
		          SELECT 1 FROM projects p WHERE p.org_id = $2 AND (cc.project_id IS NULL)))
		    OR (cc.scope = 'PROJECT' AND cc.project_id IS NOT NULL)
		  )
		ORDER BY
		  CASE cc.scope WHEN 'AGENT' THEN 1 WHEN 'ORG' THEN 2 WHEN 'PROJECT' THEN 3 END,
		  cc.updated_at DESC
	`, projectFilter, chunkTypeFilter, queryKeyFilter)

	rows, err := t.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("fetchInjectAudienceCandidates: %w", err)
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
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &rawContent, &c.Scope, &c.ChunkType, &iaRaw); err != nil {
			return nil, 0, err
		}
		c.Content = decodeContent(rawContent)
		candidates = append(candidates, struct {
			InjectedChunk
			iaRaw []byte
		}{InjectedChunk: c, iaRaw: iaRaw})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var chunks []InjectedChunk
	for _, cand := range candidates {
		if len(cand.iaRaw) == 0 {
			continue
		}
		var ia models.InjectAudience
		if err := json.Unmarshal(cand.iaRaw, &ia); err != nil {
			continue
		}
		lifecycleStr := ""
		if lifecycleType != nil {
			lifecycleStr = *lifecycleType
		}
		if ia.MatchesSession(agentID, agentType, lifecycleStr, orgID, agentTags, nil) {
			chunks = append(chunks, cand.InjectedChunk)
		}
	}

	truncated := 0
	if maxInjectTokens > 0 {
		var kept []InjectedChunk
		var discarded []InjectedChunk
		runningTokens := 0
		for _, chunk := range chunks {
			estTokens := len(chunk.Content) / 4
			if runningTokens+estTokens <= maxInjectTokens {
				kept = append(kept, chunk)
				runningTokens += estTokens
			} else {
				discarded = append(discarded, chunk)
			}
		}
		truncated = len(discarded)
		chunks = kept
	}

	return chunks, truncated, nil
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
		return nil, fmt.Errorf("agent already has an active session — call resume_session instead: %w", err)
	}

	typeFilters := t.resolveAgentInjectFilters(ctx, agentID)
	scopes := lcCfg.InjectScopes
	if len(typeFilters.IncludeScopes) > 0 {
		scopes = intersectScopes(scopes, typeFilters.IncludeScopes)
	}

	agentTags := t.resolveAgentTags(ctx, in.AgentID)
	chunks, truncated, err := t.fetchInjectAudienceCandidates(
		ctx, in.AgentID, t.resolveAgentType(ctx, in.AgentID), agentTags, &lifecycleSlug, in.ProjectID, orgID, scopes,
		typeFilters.IncludeChunkTypes,
		typeFilters.ExcludeChunkTypes,
		typeFilters.ExcludeQueryKeys,
		typeFilters.MaxInjectTokens,
	)
	if err != nil {
		return nil, err
	}

	t.cacheInjectSet(ctx, sessionID, chunks)

	result := &StartSessionResult{
		SessionID:      sessionID,
		ExpiresAt:      expiresAt,
		Lifecycle:      lc.Slug,
		RequiredSteps:  lcCfg.RequiredSteps,
		InjectedChunks: chunks,
	}
	if truncated > 0 {
		result.TruncatedCount = truncated
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

	// Fetch session + lifecycle in one query.
	var sess models.Session
	var lcConfig []byte
	var lcSlug *string
	err := t.pool.QueryRow(ctx, `
		SELECT s.id, s.agent_id, s.project_id, s.org_id, s.lifecycle_id,
		       s.status, s.focus_task, s.chunks_written, s.chunks_read,
		       s.consolidation_done, s.resume_count, s.expires_at,
		       s.started_at, s.ended_at, s.created_at, s.updated_at,
		       COALESCE(sl.config, '{"ttl_hours":8,"inject_scopes":["AGENT","PROJECT","ORG"]}'::jsonb) as lc_config,
		       sl.slug as lc_slug
		FROM sessions s
		LEFT JOIN session_lifecycles sl ON sl.id = s.lifecycle_id
		WHERE s.id = $1 AND s.org_id = $2
	`, in.SessionID, orgID).Scan(
		&sess.ID, &sess.AgentID, &sess.ProjectID, &sess.OrgID, &sess.LifecycleID,
		&sess.Status, &sess.FocusTask, &sess.ChunksWritten, &sess.ChunksRead,
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
	chunks, _, err := t.fetchInjectAudienceCandidates(
		ctx, sess.AgentID, t.resolveAgentType(ctx, sess.AgentID), agentTags, lcSlug, sess.ProjectID, orgID, scopes,
		typeFilters.IncludeChunkTypes,
		typeFilters.ExcludeChunkTypes,
		typeFilters.ExcludeQueryKeys,
		0,
	)
	if err != nil {
		return nil, err
	}

	t.cacheInjectSet(ctx, sess.ID, chunks)

	return &ResumeSessionResult{
		SessionID:      sess.ID,
		ExpiresAt:      newExpiry,
		FocusTask:      sess.FocusTask,
		ChunksWritten:  sess.ChunksWritten,
		ResumeCount:    sess.ResumeCount + 1,
		InjectedChunks: chunks,
	}, nil
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

	var sessionID string
	err := t.pool.QueryRow(ctx, `
		UPDATE sessions
		SET focus_task = $1, updated_at = NOW()
		WHERE id = $2 AND org_id = $3 AND status = 'active'
		RETURNING id
	`, in.Task, in.SessionID, orgID).Scan(&sessionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("active session not found")
		}
		return nil, fmt.Errorf("RegisterFocus: %w", err)
	}

	return &RegisterFocusResult{
		SessionID: sessionID,
		FocusTask: in.Task,
	}, nil
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
