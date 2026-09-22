package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/XferOps/hizal/internal/mcp"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionHandlers handles session lifecycle REST endpoints.
type SessionHandlers struct {
	tools *mcp.Tools
	pool  *pgxpool.Pool
}

func NewSessionHandlers(tools *mcp.Tools, pool *pgxpool.Pool) *SessionHandlers {
	return &SessionHandlers{tools: tools, pool: pool}
}

// resolveOrgID extracts org_id from JWT claims or API key context.
func resolveOrgID(r *http.Request) string {
	if claims, ok := ClaimsFrom(r.Context()); ok {
		return claims.OrgID
	}
	return ""
}

// resolveOrgIDFromSession resolves the org_id for session-scoped endpoints.
// API key path: reads from context (fast, no DB).
// JWT user path: looks up the session's org_id from DB and verifies the user is a member.
func (h *SessionHandlers) resolveOrgIDFromSession(r *http.Request, sessionID string) (string, error) {
	if orgID := resolveOrgID(r); orgID != "" {
		return orgID, nil
	}
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		return "", fmt.Errorf("unauthenticated")
	}
	var orgID string
	err := h.pool.QueryRow(r.Context(), `
		SELECT s.org_id
		FROM sessions s
		JOIN org_memberships m ON m.org_id = s.org_id AND m.user_id = $2
		WHERE s.id = $1
	`, sessionID, user.ID).Scan(&orgID)
	if err != nil {
		return "", fmt.Errorf("session not found or access denied")
	}
	return orgID, nil
}

// POST /v1/sessions
// Body: { agent_id, project_id?, lifecycle_slug?, chunk_detail? }
// agent_id is required in the REST body (JWT/human path — caller specifies which agent).
func (h *SessionHandlers) StartSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID       string  `json:"agent_id"`
		ProjectID     *string `json:"project_id,omitempty"`
		LifecycleSlug *string `json:"lifecycle_slug,omitempty"`
		ChunkDetail   *string `json:"chunk_detail,omitempty"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}
	if body.AgentID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "agent_id is required")
		return
	}
	orgID := resolveOrgID(r)
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "org context required")
		return
	}
	in := mcp.StartSessionInput{
		ProjectID:     body.ProjectID,
		LifecycleSlug: body.LifecycleSlug,
		ChunkDetail:   body.ChunkDetail,
	}
	result, err := h.tools.StartSession(r.Context(), orgID, body.AgentID, in)
	if err != nil {
		writeError(w, http.StatusConflict, "SESSION_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// POST /v1/sessions/:id/resume
// Body (optional): { chunk_detail? }
func (h *SessionHandlers) ResumeSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	orgID, err := h.resolveOrgIDFromSession(r, sessionID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", err.Error())
		return
	}
	var body struct {
		ChunkDetail *string `json:"chunk_detail,omitempty"`
	}
	// Body is optional — a bare POST resumes with full detail.
	if r.ContentLength != 0 {
		if err := decodeJSONBody(r, &body); err != nil {
			writeJSONDecodeError(w, err, "")
			return
		}
	}
	result, err := h.tools.ResumeSession(r.Context(), orgID, mcp.ResumeSessionInput{SessionID: sessionID, ChunkDetail: body.ChunkDetail})
	if err != nil {
		writeError(w, http.StatusBadRequest, "RESUME_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /v1/sessions/:id/focus
// Body: { task, tags? }
func (h *SessionHandlers) RegisterFocus(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	var body struct {
		Task string   `json:"task"`
		Tags []string `json:"tags,omitempty"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}
	orgID, err := h.resolveOrgIDFromSession(r, sessionID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", err.Error())
		return
	}
	result, err := h.tools.RegisterFocus(r.Context(), orgID, mcp.RegisterFocusInput{
		SessionID: sessionID,
		Task:      body.Task,
		Tags:      body.Tags,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "FOCUS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /v1/sessions/:id/end
func (h *SessionHandlers) EndSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	orgID, err := h.resolveOrgIDFromSession(r, sessionID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", err.Error())
		return
	}
	result, err := h.tools.EndSession(r.Context(), orgID, mcp.EndSessionInput{SessionID: sessionID})
	if err != nil {
		writeError(w, http.StatusBadRequest, "END_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /v1/orgs/:id/sessions
// Query params: ?status=active|ended|expired  (default: all)
func (h *SessionHandlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	status := r.URL.Query().Get("status")

	query := `
		SELECT s.id, s.agent_id, s.project_id, s.org_id, s.lifecycle_id,
		       s.status, s.focus_task, s.focus_tags, s.chunks_written, s.chunks_read,
		       s.consolidation_done, s.resume_count, s.expires_at,
		       s.started_at, s.ended_at, s.created_at, s.updated_at,
		       a.name AS agent_name,
		       p.name AS project_name,
		       sl.slug AS lifecycle_slug
		FROM sessions s
		LEFT JOIN agents a ON a.id = s.agent_id
		LEFT JOIN projects p ON p.id = s.project_id
		LEFT JOIN session_lifecycles sl ON sl.id = s.lifecycle_id
		WHERE s.org_id = $1`
	args := []interface{}{orgID}

	if status != "" {
		args = append(args, status)
		query += ` AND s.status = $2`
	}
	query += ` ORDER BY s.started_at DESC LIMIT 100`

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		writeInternalError(r, w, "QUERY_FAILED", err)
		return
	}
	defer rows.Close()

	type sessionRow struct {
		ID                string   `json:"id"`
		AgentID           string   `json:"agent_id"`
		AgentName         *string  `json:"agent_name,omitempty"`
		ProjectID         *string  `json:"project_id,omitempty"`
		ProjectName       *string  `json:"project_name,omitempty"`
		OrgID             string   `json:"org_id"`
		LifecycleID       *string  `json:"lifecycle_id,omitempty"`
		LifecycleSlug     *string  `json:"lifecycle_slug,omitempty"`
		Status            string   `json:"status"`
		FocusTask         *string  `json:"focus_task,omitempty"`
		FocusTags         []string `json:"focus_tags,omitempty"`
		ChunksWritten     int      `json:"chunks_written"`
		ChunksRead        int      `json:"chunks_read"`
		ConsolidationDone bool     `json:"consolidation_done"`
		ResumeCount       int      `json:"resume_count"`
		ExpiresAt         string   `json:"expires_at"`
		StartedAt         string   `json:"started_at"`
		EndedAt           *string  `json:"ended_at,omitempty"`
		CreatedAt         string   `json:"created_at"`
		UpdatedAt         string   `json:"updated_at"`
	}

	sessions := []sessionRow{}
	for rows.Next() {
		var s sessionRow
		var expiresAt, startedAt, createdAt, updatedAt interface{}
		var endedAt interface{}
		err := rows.Scan(
			&s.ID, &s.AgentID, &s.ProjectID, &s.OrgID, &s.LifecycleID,
			&s.Status, &s.FocusTask, &s.FocusTags, &s.ChunksWritten, &s.ChunksRead,
			&s.ConsolidationDone, &s.ResumeCount, &expiresAt,
			&startedAt, &endedAt, &createdAt, &updatedAt,
			&s.AgentName, &s.ProjectName, &s.LifecycleSlug,
		)
		if err != nil {
			writeInternalError(r, w, "SCAN_FAILED", err)
			return
		}
		s.ExpiresAt = formatTime(expiresAt)
		s.StartedAt = formatTime(startedAt)
		s.CreatedAt = formatTime(createdAt)
		s.UpdatedAt = formatTime(updatedAt)
		if endedAt != nil {
			t := formatTime(endedAt)
			s.EndedAt = &t
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(r, w, "ROWS_ERR", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// GET /v1/sessions/:id/memory-chunks
// Deprecated: use /consolidation-chunks instead.
// Returns chunks written during this session whose type has consolidation_behavior=SURFACE.
func (h *SessionHandlers) GetSessionMemoryChunks(w http.ResponseWriter, r *http.Request) {
	h.GetSessionConsolidationChunks(w, r)
}

// GET /v1/sessions/:id/consolidation-chunks
// Returns chunks written during this session whose type has consolidation_behavior=SURFACE (for consolidation review).
func (h *SessionHandlers) GetSessionConsolidationChunks(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	orgID, err := h.resolveOrgIDFromSession(r, sessionID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT cc.id, cc.query_key, cc.title, cc.scope, cc.chunk_type, cc.created_at
		FROM context_chunks cc
		JOIN chunk_types ct ON ct.slug = cc.chunk_type
		WHERE cc.agent_id = (SELECT agent_id FROM sessions WHERE id = $1 AND org_id = $2)
		  AND (ct.org_id IS NULL OR ct.org_id = $2)
		  AND ct.consolidation_behavior = 'SURFACE'
		  AND cc.created_at >= (SELECT started_at FROM sessions WHERE id = $1 AND org_id = $2)
		ORDER BY cc.created_at ASC
	`, sessionID, orgID)
	if err != nil {
		writeInternalError(r, w, "QUERY_FAILED", err)
		return
	}
	defer rows.Close()

	type chunkSummary struct {
		ID        string `json:"id"`
		QueryKey  string `json:"query_key"`
		Title     string `json:"title"`
		Scope     string `json:"scope"`
		ChunkType string `json:"chunk_type"`
		CreatedAt string `json:"created_at"`
	}

	chunks := []chunkSummary{}
	for rows.Next() {
		var c chunkSummary
		var createdAt interface{}
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &c.Scope, &c.ChunkType, &createdAt); err != nil {
			writeInternalError(r, w, "SCAN_FAILED", err)
			return
		}
		c.CreatedAt = formatTime(createdAt)
		chunks = append(chunks, c)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_id": sessionID,
		"chunks":     chunks,
		"total":      len(chunks),
	})
}

// POST /v1/sessions/:id/consolidate
// Body: { actions: [{chunk_id, action: "keep"|"promote"|"discard", promote_to_principle?: bool}] }
// Processes KEEP/PROMOTE/DISCARD decisions for session MEMORY chunks.
func (h *SessionHandlers) ConsolidateSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	orgID, err := h.resolveOrgIDFromSession(r, sessionID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", err.Error())
		return
	}

	var body struct {
		Actions []struct {
			ChunkID            string `json:"chunk_id"`
			Action             string `json:"action"` // keep | promote | discard
			PromoteToPrinciple bool   `json:"promote_to_principle,omitempty"`
		} `json:"actions"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}

	// Verify session belongs to org
	var agentID string
	var projectID *string
	err = h.pool.QueryRow(r.Context(), `
		SELECT agent_id, project_id FROM sessions WHERE id = $1 AND org_id = $2
	`, sessionID, orgID).Scan(&agentID, &projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "session not found")
		return
	}

	kept, promoted, discarded := 0, 0, 0
	for _, a := range body.Actions {
		switch a.Action {
		case "keep":
			// No change — chunk stays as AGENT-scoped MEMORY.
			kept++
		case "promote":
			// Elevate to PROJECT-scoped KNOWLEDGE (or ORG PRINCIPLE if promote_to_principle).
			if a.PromoteToPrinciple {
				_, err = h.pool.Exec(r.Context(), `
					UPDATE context_chunks
					SET scope = 'ORG', chunk_type = 'PRINCIPLE', inject_audience = '{"rules":[{"all":true}]}'::jsonb,
					    project_id = NULL, updated_at = NOW()
					WHERE id = $1
				`, a.ChunkID)
			} else {
				_, err = h.pool.Exec(r.Context(), `
					UPDATE context_chunks
					SET scope = 'PROJECT', chunk_type = 'KNOWLEDGE', inject_audience = NULL,
					    project_id = $2, updated_at = NOW()
					WHERE id = $1
				`, a.ChunkID, projectID)
			}
			if err != nil {
				writeInternalError(r, w, "PROMOTE_FAILED", err)
				return
			}
			promoted++
		case "discard":
			_, err = h.pool.Exec(r.Context(), `DELETE FROM context_chunks WHERE id = $1`, a.ChunkID)
			if err != nil {
				writeInternalError(r, w, "DISCARD_FAILED", err)
				return
			}
			discarded++
		default:
			writeError(w, http.StatusBadRequest, "INVALID_ACTION", "action must be keep, promote, or discard")
			return
		}
	}

	// Mark session consolidation done
	_, _ = h.pool.Exec(r.Context(), `
		UPDATE sessions SET consolidation_done = TRUE, updated_at = NOW() WHERE id = $1
	`, sessionID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_id": sessionID,
		"kept":       kept,
		"promoted":   promoted,
		"discarded":  discarded,
	})
}

// POST /v1/orgs/:id/session-lifecycles
func (h *SessionHandlers) CreateSessionLifecycle(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name        string                 `json:"name"`
		Slug        string                 `json:"slug"`
		Description string                 `json:"description"`
		IsDefault   bool                   `json:"is_default"`
		Config      map[string]interface{} `json:"config"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "name and slug are required")
		return
	}
	if body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}

	for _, c := range body.Slug {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			writeError(w, http.StatusBadRequest, "INVALID_SLUG", "slug must be lowercase alphanumeric with hyphens only")
			return
		}
	}

	if body.Config == nil {
		body.Config = map[string]interface{}{}
	}

	ttlHours, ok := body.Config["ttl_hours"].(float64)
	if !ok || ttlHours <= 0 {
		writeError(w, http.StatusBadRequest, "INVALID_CONFIG", "config.ttl_hours must be a number > 0")
		return
	}

	injectScopes, ok := body.Config["inject_scopes"].([]interface{})
	if ok {
		for _, s := range injectScopes {
			scope, ok := s.(string)
			if !ok || (scope != "PROJECT" && scope != "ORG" && scope != "AGENT") {
				writeError(w, http.StatusBadRequest, "INVALID_CONFIG", "inject_scopes must contain only PROJECT, ORG, or AGENT")
				return
			}
		}
	}

	configJSON, _ := json.Marshal(body.Config)

	var lc struct {
		ID        string      `json:"id"`
		OrgID     *string     `json:"org_id"`
		Name      string      `json:"name"`
		Slug      string      `json:"slug"`
		IsDefault bool        `json:"is_default"`
		Config    interface{} `json:"config"`
		IsGlobal  bool        `json:"is_global"`
	}
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO session_lifecycles (org_id, name, slug, is_default, description, config)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, org_id, name, slug, is_default, config, created_at, updated_at
	`, orgID, body.Name, body.Slug, body.IsDefault, nullableStr(body.Description), configJSON).Scan(
		&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &configJSON, new(time.Time), new(time.Time),
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "a session lifecycle with that slug already exists in this org")
			return
		}
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	lc.IsGlobal = lc.OrgID == nil
	_ = json.Unmarshal(configJSON, &lc.Config)

	writeJSON(w, http.StatusCreated, lc)
}

// GET /v1/session-lifecycles/:id
func (h *SessionHandlers) GetSessionLifecycle(w http.ResponseWriter, r *http.Request) {
	lcID := chi.URLParam(r, "id")

	var lc struct {
		ID        string      `json:"id"`
		OrgID     *string     `json:"org_id,omitempty"`
		Name      string      `json:"name"`
		Slug      string      `json:"slug"`
		IsDefault bool        `json:"is_default"`
		Config    interface{} `json:"config"`
		IsGlobal  bool        `json:"is_global"`
	}
	var configRaw []byte
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, name, slug, is_default, config, created_at, updated_at
		FROM session_lifecycles WHERE id = $1
	`, lcID).Scan(&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &configRaw, new(time.Time), new(time.Time))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "session lifecycle not found")
		return
	}

	if lc.OrgID != nil {
		if _, err := requireOrgRole(r, h.pool, *lc.OrgID, "owner", "admin", "member", "viewer"); err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
	}

	lc.IsGlobal = lc.OrgID == nil
	if err := json.Unmarshal(configRaw, &lc.Config); err != nil {
		lc.Config = string(configRaw)
	}

	writeJSON(w, http.StatusOK, lc)
}

// PATCH /v1/session-lifecycles/:id
func (h *SessionHandlers) UpdateSessionLifecycle(w http.ResponseWriter, r *http.Request) {
	lcID := chi.URLParam(r, "id")

	var orgID *string
	err := h.pool.QueryRow(r.Context(), `SELECT org_id FROM session_lifecycles WHERE id = $1`, lcID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "session lifecycle not found")
		return
	}

	if orgID == nil {
		writeError(w, http.StatusForbidden, "GLOBAL_LIFECYCLE", "global built-in lifecycles cannot be modified")
		return
	}

	if _, err := requireOrgRole(r, h.pool, *orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Name        *string                `json:"name"`
		Description *string                `json:"description"`
		IsDefault   *bool                  `json:"is_default"`
		Config      map[string]interface{} `json:"config"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}

	if body.Config != nil {
		if ttlHours, ok := body.Config["ttl_hours"].(float64); ok && ttlHours <= 0 {
			writeError(w, http.StatusBadRequest, "INVALID_CONFIG", "config.ttl_hours must be a number > 0")
			return
		}
		if injectScopes, ok := body.Config["inject_scopes"].([]interface{}); ok {
			for _, s := range injectScopes {
				scope, ok := s.(string)
				if !ok || (scope != "PROJECT" && scope != "ORG" && scope != "AGENT") {
					writeError(w, http.StatusBadRequest, "INVALID_CONFIG", "inject_scopes must contain only PROJECT, ORG, or AGENT")
					return
				}
			}
		}
	}

	setClauses := []string{}
	args := []any{}
	idx := 1

	// SECURITY: Column names are hardcoded strings, not derived from user input.
	// User input only controls which fields are present (non-nil) and their values.
	// Values are passed as parameterized arguments ($1, $2, etc.), preventing SQL injection.
	if body.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", idx))
		args = append(args, *body.Name)
		idx++
	}
	if body.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", idx))
		args = append(args, *body.Description)
		idx++
	}
	if body.IsDefault != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_default = $%d", idx))
		args = append(args, *body.IsDefault)
		idx++
	}
	if body.Config != nil {
		configJSON, _ := json.Marshal(body.Config)
		setClauses = append(setClauses, fmt.Sprintf("config = $%d", idx))
		args = append(args, configJSON)
		idx++
	}

	if len(setClauses) > 0 {
		setClauses = append(setClauses, "updated_at = NOW()")
		args = append(args, lcID)
		query := fmt.Sprintf("UPDATE session_lifecycles SET %s WHERE id = $%d", joinStrings(setClauses, ", "), idx)
		_, err = h.pool.Exec(r.Context(), query, args...)
		if err != nil {
			writeInternalError(r, w, "DB_ERROR", err)
			return
		}
	}

	var lc struct {
		ID        string      `json:"id"`
		OrgID     *string     `json:"org_id,omitempty"`
		Name      string      `json:"name"`
		Slug      string      `json:"slug"`
		IsDefault bool        `json:"is_default"`
		Config    interface{} `json:"config"`
		IsGlobal  bool        `json:"is_global"`
	}
	var configRaw []byte
	err = h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, name, slug, is_default, config, created_at, updated_at
		FROM session_lifecycles WHERE id = $1
	`, lcID).Scan(&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &configRaw, new(time.Time), new(time.Time))
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	lc.IsGlobal = lc.OrgID == nil
	if err := json.Unmarshal(configRaw, &lc.Config); err != nil {
		lc.Config = string(configRaw)
	}

	writeJSON(w, http.StatusOK, lc)
}

// DELETE /v1/session-lifecycles/:id
func (h *SessionHandlers) DeleteSessionLifecycle(w http.ResponseWriter, r *http.Request) {
	lcID := chi.URLParam(r, "id")

	var orgID *string
	err := h.pool.QueryRow(r.Context(), `SELECT org_id FROM session_lifecycles WHERE id = $1`, lcID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "session lifecycle not found")
		return
	}

	if orgID == nil {
		writeError(w, http.StatusForbidden, "GLOBAL_LIFECYCLE", "global built-in lifecycles cannot be deleted")
		return
	}

	if _, err := requireOrgRole(r, h.pool, *orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	_, err = h.pool.Exec(r.Context(), `DELETE FROM session_lifecycles WHERE id = $1`, lcID)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /v1/orgs/:id/session-lifecycles
func (h *SessionHandlers) ListSessionLifecycles(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")

	rows, err := h.pool.Query(r.Context(), `
		SELECT
			sl.id, sl.org_id, sl.name, sl.slug, sl.is_default, sl.description,
			sl.config, sl.hidden, sl.created_at, sl.updated_at,
			g.id AS overrides_global_id
		FROM session_lifecycles sl
		LEFT JOIN session_lifecycles g
			ON g.slug = sl.slug AND g.org_id IS NULL AND sl.org_id = $1
		WHERE (
			-- org shadow rows (not hidden)
			(sl.org_id = $1 AND sl.hidden = false)
			OR
			-- globals with no org shadow
			(sl.org_id IS NULL AND NOT EXISTS (
				SELECT 1 FROM session_lifecycles shadow
				WHERE shadow.org_id = $1 AND shadow.slug = sl.slug
			))
		)
		ORDER BY sl.org_id NULLS FIRST, sl.name
	`, orgID)
	if err != nil {
		writeInternalError(r, w, "QUERY_FAILED", err)
		return
	}
	defer rows.Close()

	type lcRow struct {
		ID              string      `json:"id"`
		OrgID           *string     `json:"org_id,omitempty"`
		Name            string      `json:"name"`
		Slug            string      `json:"slug"`
		IsDefault       bool        `json:"is_default"`
		Description     *string     `json:"description,omitempty"`
		Config          interface{} `json:"config"`
		Hidden          bool        `json:"hidden"`
		IsGlobal        bool        `json:"is_global"`
		OverridesGlobal *string     `json:"overrides_global,omitempty"`
	}

	lifecycles := []lcRow{}
	for rows.Next() {
		var lc lcRow
		var configRaw []byte
		var createdAt, updatedAt interface{}
		var overridesGlobalID *string
		if err := rows.Scan(&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &lc.Description, &configRaw, &lc.Hidden, &createdAt, &updatedAt, &overridesGlobalID); err != nil {
			writeInternalError(r, w, "SCAN_FAILED", err)
			return
		}
		lc.IsGlobal = lc.OrgID == nil
		if overridesGlobalID != nil {
			lc.OverridesGlobal = overridesGlobalID
		}
		if err := json.Unmarshal(configRaw, &lc.Config); err != nil {
			lc.Config = string(configRaw)
		}
		lifecycles = append(lifecycles, lc)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"lifecycles": lifecycles,
	})
}

// POST /v1/orgs/:id/session-lifecycles/:slug/fork
func (h *SessionHandlers) ForkSessionLifecycle(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	slug := chi.URLParam(r, "slug")

	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var globalID string
	var globalName, globalSlug string
	var globalDesc *string
	var globalIsDefault bool
	var globalConfig []byte
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, name, slug, description, is_default, config
		FROM session_lifecycles WHERE org_id IS NULL AND slug = $1
	`, slug).Scan(
		&globalID, &globalName, &globalSlug, &globalDesc, &globalIsDefault, &globalConfig,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "global session lifecycle not found")
		return
	}

	var existingID string
	err = h.pool.QueryRow(r.Context(), `SELECT id FROM session_lifecycles WHERE org_id = $1 AND slug = $2`, orgID, slug).Scan(&existingID)
	if err == nil {
		writeError(w, http.StatusConflict, "ALREADY_OVERRIDDEN", "org already has an override for this session lifecycle")
		return
	}

	var lc struct {
		ID        string      `json:"id"`
		OrgID     *string     `json:"org_id,omitempty"`
		Name      string      `json:"name"`
		Slug      string      `json:"slug"`
		IsDefault bool        `json:"is_default"`
		Config    interface{} `json:"config"`
		IsGlobal  bool        `json:"is_global"`
	}
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO session_lifecycles (org_id, name, slug, is_default, description, config, hidden)
		VALUES ($1, $2, $3, $4, $5, $6, false)
		RETURNING id, org_id, name, slug, is_default, config, created_at, updated_at
	`, orgID, globalName, globalSlug, globalIsDefault, nullableStr(*globalDesc), globalConfig).Scan(
		&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &globalConfig, new(time.Time), new(time.Time),
	)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	lc.IsGlobal = false
	_ = json.Unmarshal(globalConfig, &lc.Config)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"lifecycle":           lc,
		"overrides_global":   globalID,
	})
}

// POST /v1/orgs/:id/session-lifecycles/:slug/reset?action=hide|reset
func (h *SessionHandlers) ResetOrHideSessionLifecycle(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	slug := chi.URLParam(r, "slug")
	action := r.URL.Query().Get("action")

	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var globalID string
	err := h.pool.QueryRow(r.Context(), `SELECT id FROM session_lifecycles WHERE org_id IS NULL AND slug = $1`, slug).Scan(&globalID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "global session lifecycle not found")
		return
	}

	if action == "hide" {
		var existingID string
		var existingHidden bool
		err = h.pool.QueryRow(r.Context(), `SELECT id, hidden FROM session_lifecycles WHERE org_id = $1 AND slug = $2`, orgID, slug).Scan(&existingID, &existingHidden)
		if err == nil {
			if existingHidden {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, err = h.pool.Exec(r.Context(), `UPDATE session_lifecycles SET hidden = true WHERE id = $1`, existingID)
			if err != nil {
				writeInternalError(r, w, "DB_ERROR", err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var globalName, globalSlug string
		var globalDesc *string
		var globalIsDefault bool
		var globalConfig []byte
		err = h.pool.QueryRow(r.Context(), `
			SELECT name, slug, description, is_default, config
			FROM session_lifecycles WHERE org_id IS NULL AND slug = $1
		`, slug).Scan(
			&globalName, &globalSlug, &globalDesc, &globalIsDefault, &globalConfig,
		)
		if err != nil {
			writeInternalError(r, w, "DB_ERROR", err)
			return
		}

		_, err = h.pool.Exec(r.Context(), `
			INSERT INTO session_lifecycles (org_id, name, slug, is_default, description, config, hidden)
			VALUES ($1, $2, $3, $4, $5, $6, true)
		`, orgID, globalName, globalSlug, globalIsDefault, nullableStr(*globalDesc), globalConfig)
		if err != nil {
			writeInternalError(r, w, "DB_ERROR", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	_, err = h.pool.Exec(r.Context(), `DELETE FROM session_lifecycles WHERE org_id = $1 AND slug = $2`, orgID, slug)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// formatTime converts a pgx time value to RFC3339 string.
func formatTime(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case interface{ Format(string) string }:
		return t.Format("2006-01-02T15:04:05Z07:00")
	default:
		return ""
	}
}
