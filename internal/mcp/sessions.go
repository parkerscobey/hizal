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
	AgentID      string  `json:"agent_id"`
	ProjectID    *string `json:"project_id,omitempty"`
	LifecycleSlug *string `json:"lifecycle_slug,omitempty"` // defaults to "default"
}

type StartSessionResult struct {
	SessionID      string                `json:"session_id"`
	ExpiresAt      time.Time             `json:"expires_at"`
	Lifecycle      string                `json:"lifecycle"`
	RequiredSteps  []string              `json:"required_steps"`
	InjectedChunks []InjectedChunk       `json:"injected_chunks"`
	TruncatedCount int                   `json:"truncated_count,omitempty"`
}

type InjectedChunk struct {
	ID       string `json:"id"`
	QueryKey string `json:"query_key"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Scope    string `json:"scope"`
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
	SessionID     string              `json:"session_id"`
	ChunksWritten int                 `json:"chunks_written"`
	ChunksRead    int                 `json:"chunks_read"`
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

// fetchAlwaysInjectChunks returns always_inject=true chunks for the given session,
// filtered to the scopes specified in the lifecycle config.
func (t *Tools) fetchAlwaysInjectChunks(ctx context.Context, agentID string, projectID *string, orgID string, scopes []string) ([]InjectedChunk, error) {
	args := []any{agentID, orgID, scopes}
	projectFilter := "AND (cc.scope != 'PROJECT')" // no project scope if no project
	if projectID != nil {
		args = append(args, *projectID)
		projectFilter = fmt.Sprintf("AND (cc.scope != 'PROJECT' OR cc.project_id = $%d)", len(args))
	}

	query := fmt.Sprintf(`
		SELECT cc.id, cc.query_key, cc.title, cc.content, cc.scope
		FROM context_chunks cc
		WHERE cc.always_inject = TRUE
		  AND cc.scope = ANY($3)
		  %s
		  AND (
		    (cc.scope = 'AGENT' AND cc.agent_id = $1)
		    OR (cc.scope = 'ORG' AND cc.project_id IS NULL AND EXISTS (
		          SELECT 1 FROM projects p WHERE p.org_id = $2 AND (cc.project_id IS NULL)))
		    OR (cc.scope = 'PROJECT' AND cc.project_id IS NOT NULL)
		  )
		ORDER BY
		  CASE cc.scope WHEN 'AGENT' THEN 1 WHEN 'ORG' THEN 2 WHEN 'PROJECT' THEN 3 END,
		  cc.updated_at DESC
	`, projectFilter)

	rows, err := t.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetchAlwaysInjectChunks: %w", err)
	}
	defer rows.Close()

	var chunks []InjectedChunk
	for rows.Next() {
		var c InjectedChunk
		var rawContent []byte
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &rawContent, &c.Scope); err != nil {
			return nil, err
		}
		c.Content = decodeContent(rawContent)
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// ---- Tool Implementations ----

// StartSession begins a new session for an agent.
// Returns the session ID and all always_inject chunks for the agent's context window.
// Fails if the agent already has an active session (use ResumeSession instead).
func (t *Tools) StartSession(ctx context.Context, orgID string, in StartSessionInput) (*StartSessionResult, error) {
	if in.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	lifecycleSlug := "default"
	if in.LifecycleSlug != nil && *in.LifecycleSlug != "" {
		lifecycleSlug = *in.LifecycleSlug
	}

	lc, err := t.resolveLifecycle(ctx, orgID, lifecycleSlug)
	if err != nil {
		return nil, err
	}
	cfg, err := parseLifecycleConfig(lc)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(time.Duration(cfg.TTLHours) * time.Hour)

	// Insert session — the partial unique index will reject a duplicate active session.
	var sessionID string
	err = t.pool.QueryRow(ctx, `
		INSERT INTO sessions (agent_id, project_id, org_id, lifecycle_id, status, expires_at)
		VALUES ($1, $2, $3, $4, 'active', $5)
		RETURNING id
	`, in.AgentID, in.ProjectID, orgID, lc.ID, expiresAt).Scan(&sessionID)
	if err != nil {
		// Unique constraint violation = already has an active session.
		return nil, fmt.Errorf("agent already has an active session — call resume_session instead: %w", err)
	}

	// Fetch always_inject chunks.
	chunks, err := t.fetchAlwaysInjectChunks(ctx, in.AgentID, in.ProjectID, orgID, cfg.InjectScopes)
	if err != nil {
		return nil, err
	}

	return &StartSessionResult{
		SessionID:      sessionID,
		ExpiresAt:      expiresAt,
		Lifecycle:      lc.Slug,
		RequiredSteps:  cfg.RequiredSteps,
		InjectedChunks: chunks,
	}, nil
}

// ResumeSession extends an existing active session's TTL and re-injects
// always_inject chunks fresh. Use after a break or when resuming across
// tool calls. Extends TTL by the lifecycle's ttl_hours from now.
func (t *Tools) ResumeSession(ctx context.Context, orgID string, in ResumeSessionInput) (*ResumeSessionResult, error) {
	if in.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	// Fetch session + lifecycle in one query.
	var sess models.Session
	var lcConfig []byte
	var lcScopes []byte // we'll parse from config
	err := t.pool.QueryRow(ctx, `
		SELECT s.id, s.agent_id, s.project_id, s.org_id, s.lifecycle_id,
		       s.status, s.focus_task, s.chunks_written, s.chunks_read,
		       s.consolidation_done, s.resume_count, s.expires_at,
		       s.started_at, s.ended_at, s.created_at, s.updated_at,
		       COALESCE(sl.config, '{"ttl_hours":8,"inject_scopes":["AGENT","PROJECT","ORG"]}'::jsonb) as lc_config
		FROM sessions s
		LEFT JOIN session_lifecycles sl ON sl.id = s.lifecycle_id
		WHERE s.id = $1 AND s.org_id = $2
	`, in.SessionID, orgID).Scan(
		&sess.ID, &sess.AgentID, &sess.ProjectID, &sess.OrgID, &sess.LifecycleID,
		&sess.Status, &sess.FocusTask, &sess.ChunksWritten, &sess.ChunksRead,
		&sess.ConsolidationDone, &sess.ResumeCount, &sess.ExpiresAt,
		&sess.StartedAt, &sess.EndedAt, &sess.CreatedAt, &sess.UpdatedAt,
		&lcConfig,
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

	_ = lcScopes // suppress unused

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

	// Re-inject always_inject chunks fresh.
	chunks, err := t.fetchAlwaysInjectChunks(ctx, sess.AgentID, sess.ProjectID, orgID, cfg.InjectScopes)
	if err != nil {
		return nil, err
	}

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

	// Return write_memory chunks written during this session for consolidation review.
	// These are AGENT-scoped, always_inject=false (episodic) — the ones to classify.
	rows, err := t.pool.Query(ctx, `
		SELECT id, query_key, title, scope
		FROM context_chunks
		WHERE agent_id = (SELECT agent_id FROM sessions WHERE id = $1)
		  AND chunk_type = 'MEMORY'
		  AND always_inject = FALSE
		  AND created_at >= (SELECT started_at FROM sessions WHERE id = $1)
		ORDER BY created_at ASC
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
