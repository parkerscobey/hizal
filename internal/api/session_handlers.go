package api

import (
	"encoding/json"
	"net/http"

	"github.com/XferOps/winnow/internal/mcp"
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

// POST /v1/sessions
// Body: { agent_id, project_id?, lifecycle_slug? }
func (h *SessionHandlers) StartSession(w http.ResponseWriter, r *http.Request) {
	var in mcp.StartSessionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	orgID := resolveOrgID(r)
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "org context required")
		return
	}
	result, err := h.tools.StartSession(r.Context(), orgID, in)
	if err != nil {
		writeError(w, http.StatusConflict, "SESSION_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// POST /v1/sessions/:id/resume
func (h *SessionHandlers) ResumeSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	orgID := resolveOrgID(r)
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "org context required")
		return
	}
	result, err := h.tools.ResumeSession(r.Context(), orgID, mcp.ResumeSessionInput{SessionID: sessionID})
	if err != nil {
		writeError(w, http.StatusBadRequest, "RESUME_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /v1/sessions/:id/focus
// Body: { task }
func (h *SessionHandlers) RegisterFocus(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	var body struct {
		Task string `json:"task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	orgID := resolveOrgID(r)
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "org context required")
		return
	}
	result, err := h.tools.RegisterFocus(r.Context(), orgID, mcp.RegisterFocusInput{
		SessionID: sessionID,
		Task:      body.Task,
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
	orgID := resolveOrgID(r)
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "org context required")
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
		       s.status, s.focus_task, s.chunks_written, s.chunks_read,
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
		writeError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error())
		return
	}
	defer rows.Close()

	type sessionRow struct {
		ID                string  `json:"id"`
		AgentID           string  `json:"agent_id"`
		AgentName         *string `json:"agent_name,omitempty"`
		ProjectID         *string `json:"project_id,omitempty"`
		ProjectName       *string `json:"project_name,omitempty"`
		OrgID             string  `json:"org_id"`
		LifecycleID       *string `json:"lifecycle_id,omitempty"`
		LifecycleSlug     *string `json:"lifecycle_slug,omitempty"`
		Status            string  `json:"status"`
		FocusTask         *string `json:"focus_task,omitempty"`
		ChunksWritten     int     `json:"chunks_written"`
		ChunksRead        int     `json:"chunks_read"`
		ConsolidationDone bool    `json:"consolidation_done"`
		ResumeCount       int     `json:"resume_count"`
		ExpiresAt         string  `json:"expires_at"`
		StartedAt         string  `json:"started_at"`
		EndedAt           *string `json:"ended_at,omitempty"`
		CreatedAt         string  `json:"created_at"`
		UpdatedAt         string  `json:"updated_at"`
	}

	sessions := []sessionRow{}
	for rows.Next() {
		var s sessionRow
		var expiresAt, startedAt, createdAt, updatedAt interface{}
		var endedAt interface{}
		err := rows.Scan(
			&s.ID, &s.AgentID, &s.ProjectID, &s.OrgID, &s.LifecycleID,
			&s.Status, &s.FocusTask, &s.ChunksWritten, &s.ChunksRead,
			&s.ConsolidationDone, &s.ResumeCount, &expiresAt,
			&startedAt, &endedAt, &createdAt, &updatedAt,
			&s.AgentName, &s.ProjectName, &s.LifecycleSlug,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "SCAN_FAILED", err.Error())
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
		writeError(w, http.StatusInternalServerError, "ROWS_ERR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// GET /v1/sessions/:id/memory-chunks
// Returns MEMORY chunks written during this session (for consolidation review).
func (h *SessionHandlers) GetSessionMemoryChunks(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	orgID := resolveOrgID(r)

	rows, err := h.pool.Query(r.Context(), `
		SELECT cc.id, cc.query_key, cc.title, cc.scope, cc.chunk_type, cc.always_inject, cc.created_at
		FROM context_chunks cc
		WHERE cc.agent_id = (SELECT agent_id FROM sessions WHERE id = $1 AND org_id = $2)
		  AND cc.chunk_type = 'MEMORY'
		  AND cc.always_inject = FALSE
		  AND cc.created_at >= (SELECT started_at FROM sessions WHERE id = $1 AND org_id = $2)
		ORDER BY cc.created_at ASC
	`, sessionID, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error())
		return
	}
	defer rows.Close()

	type chunkSummary struct {
		ID           string `json:"id"`
		QueryKey     string `json:"query_key"`
		Title        string `json:"title"`
		Scope        string `json:"scope"`
		ChunkType    string `json:"chunk_type"`
		AlwaysInject bool   `json:"always_inject"`
		CreatedAt    string `json:"created_at"`
	}

	chunks := []chunkSummary{}
	for rows.Next() {
		var c chunkSummary
		var createdAt interface{}
		if err := rows.Scan(&c.ID, &c.QueryKey, &c.Title, &c.Scope, &c.ChunkType, &c.AlwaysInject, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "SCAN_FAILED", err.Error())
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
	orgID := resolveOrgID(r)

	var body struct {
		Actions []struct {
			ChunkID           string `json:"chunk_id"`
			Action            string `json:"action"` // keep | promote | discard
			PromoteToPrinciple bool  `json:"promote_to_principle,omitempty"`
		} `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	// Verify session belongs to org
	var agentID string
	var projectID *string
	err := h.pool.QueryRow(r.Context(), `
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
					SET scope = 'ORG', chunk_type = 'PRINCIPLE', always_inject = TRUE,
					    project_id = NULL, updated_at = NOW()
					WHERE id = $1
				`, a.ChunkID)
			} else {
				_, err = h.pool.Exec(r.Context(), `
					UPDATE context_chunks
					SET scope = 'PROJECT', chunk_type = 'KNOWLEDGE', always_inject = FALSE,
					    project_id = $2, updated_at = NOW()
					WHERE id = $1
				`, a.ChunkID, projectID)
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "PROMOTE_FAILED", err.Error())
				return
			}
			promoted++
		case "discard":
			_, err = h.pool.Exec(r.Context(), `DELETE FROM context_chunks WHERE id = $1`, a.ChunkID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "DISCARD_FAILED", err.Error())
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

// GET /v1/orgs/:id/session-lifecycles
func (h *SessionHandlers) ListSessionLifecycles(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, org_id, name, slug, is_default, config, created_at, updated_at
		FROM session_lifecycles
		WHERE org_id = $1 OR org_id IS NULL
		ORDER BY org_id NULLS FIRST, name
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error())
		return
	}
	defer rows.Close()

	type lcRow struct {
		ID        string      `json:"id"`
		OrgID     *string     `json:"org_id,omitempty"`
		Name      string      `json:"name"`
		Slug      string      `json:"slug"`
		IsDefault bool        `json:"is_default"`
		Config    interface{} `json:"config"`
		IsGlobal  bool        `json:"is_global"`
	}

	lifecycles := []lcRow{}
	for rows.Next() {
		var lc lcRow
		var configRaw []byte
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&lc.ID, &lc.OrgID, &lc.Name, &lc.Slug, &lc.IsDefault, &configRaw, &createdAt, &updatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "SCAN_FAILED", err.Error())
			return
		}
		lc.IsGlobal = lc.OrgID == nil
		if err := json.Unmarshal(configRaw, &lc.Config); err != nil {
			lc.Config = string(configRaw)
		}
		lifecycles = append(lifecycles, lc)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"lifecycles": lifecycles,
	})
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
