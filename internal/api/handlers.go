package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/XferOps/hizal/internal/auth"
	"github.com/XferOps/hizal/internal/mcp"
	"github.com/XferOps/hizal/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ContextReviewItem struct {
	ID              string    `json:"id"`
	ChunkID         string    `json:"chunk_id"`
	Task            *string   `json:"task,omitempty"`
	Usefulness      *int      `json:"usefulness,omitempty"`
	UsefulnessNote  *string   `json:"usefulness_note,omitempty"`
	Correctness     *int      `json:"correctness,omitempty"`
	CorrectnessNote *string   `json:"correctness_note,omitempty"`
	Action          *string   `json:"action,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type ContextReviewsResponse struct {
	Reviews []ContextReviewItem `json:"reviews"`
	Total   int                 `json:"total"`
}

type Handlers struct {
	tools *mcp.Tools
	pool  *pgxpool.Pool
}

func NewHandlers(tools *mcp.Tools, pool *pgxpool.Pool) *Handlers {
	return &Handlers{tools: tools, pool: pool}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]string{"code": code, "message": msg},
	})
}

func projectID(r *http.Request) string {
	claims, ok := ClaimsFrom(r.Context())
	if !ok {
		if projectID := r.URL.Query().Get("project_id"); projectID != "" {
			return projectID
		}
		return r.Header.Get("X-Project-ID")
	}
	if claims.ProjectID != "" {
		return claims.ProjectID
	}
	if projectID := r.URL.Query().Get("project_id"); projectID != "" {
		return projectID
	}
	return r.Header.Get("X-Project-ID")
}

func orgID(r *http.Request) string {
	claims, ok := ClaimsFrom(r.Context())
	if !ok {
		return r.URL.Query().Get("org_id")
	}
	if claims.OrgID != "" {
		return claims.OrgID
	}
	return r.URL.Query().Get("org_id")
}

// POST /v1/context
func (h *Handlers) WriteContext(w http.ResponseWriter, r *http.Request) {
	var in mcp.WriteContextInput
	if err := decodeJSONBody(r, &in); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}
	result, err := h.tools.WriteContext(r.Context(), projectID(r), in)
	if err != nil {
		writeInternalError(r, w, "WRITE_FAILED", err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// GET /v1/context/search
// Scope params: ?scope=PROJECT|AGENT|ORG &agent_id=&org_id=&chunk_type=&always_inject_only=true
func (h *Handlers) SearchContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	alwaysInjectOnly := q.Get("always_inject_only") == "true"
	in := mcp.SearchContextInput{
		Query:            q.Get("query"),
		Limit:            limit,
		QueryKey:         q.Get("query_key"),
		Scope:            q.Get("scope"),
		AgentID:          q.Get("agent_id"),
		OrgID:            orgID(r),
		ChunkType:        q.Get("chunk_type"),
		AlwaysInjectOnly: alwaysInjectOnly,
	}
	result, err := h.tools.SearchContext(r.Context(), projectID(r), in, models.AgentTypeFilterConfig{})
	if err != nil {
		writeInternalError(r, w, "SEARCH_FAILED", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /v1/context/compact
// Scope params: ?scope=PROJECT|AGENT|ORG &agent_id=&org_id=&chunk_type=
func (h *Handlers) CompactContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	in := mcp.CompactContextInput{
		Query:     q.Get("query"),
		Limit:     limit,
		Scope:     q.Get("scope"),
		AgentID:   q.Get("agent_id"),
		OrgID:     orgID(r),
		ChunkType: q.Get("chunk_type"),
	}
	result, err := h.tools.CompactContext(r.Context(), projectID(r), in, models.AgentTypeFilterConfig{})
	if err != nil {
		writeInternalError(r, w, "COMPACT_FAILED", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /v1/context?query_key=<key>&project_id=<uuid> or /v1/context/:id
func (h *Handlers) ReadContext(w http.ResponseWriter, r *http.Request) {
	in := mcp.ReadContextInput{
		ID:       chi.URLParam(r, "id"),
		QueryKey: r.URL.Query().Get("query_key"),
	}
	result, err := h.tools.ReadContext(r.Context(), projectID(r), in)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /v1/context/identity?agent_id=<uuid>
func (h *Handlers) GetIdentity(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	orgID := ""
	if claims, ok := ClaimsFrom(r.Context()); ok {
		orgID = claims.OrgID
		if claims.AgentID != "" {
			agentID = claims.AgentID
		}
	}

	result, err := h.tools.GetIdentity(r.Context(), mcp.GetIdentityInput{AgentID: agentID, OrgID: orgID})
	if err != nil {
		writeError(w, http.StatusBadRequest, "GET_IDENTITY_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /v1/context/:id/versions
func (h *Handlers) GetContextVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.tools.GetContextVersions(r.Context(), projectID(r), id, limit)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /v1/context/:id/reviews
func (h *Handlers) GetContextReviews(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, chunk_id, task, usefulness, usefulness_note, correctness, correctness_note, action, created_at
		FROM context_reviews
		WHERE chunk_id = $1
		ORDER BY created_at DESC
	`, id)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}
	defer rows.Close()

	reviews := make([]ContextReviewItem, 0)
	for rows.Next() {
		var item ContextReviewItem
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ChunkID, &item.Task, &item.Usefulness, &item.UsefulnessNote, &item.Correctness, &item.CorrectnessNote, &item.Action, &createdAt); err != nil {
			writeInternalError(r, w, "DB_ERROR", err)
			return
		}
		item.CreatedAt = createdAt
		reviews = append(reviews, item)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	writeJSON(w, http.StatusOK, ContextReviewsResponse{Reviews: reviews, Total: len(reviews)})
}

// PATCH /v1/context/:id
func (h *Handlers) UpdateContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var in mcp.UpdateContextInput
	if err := decodeJSONBody(r, &in); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}
	in.ID = id
	result, err := h.tools.UpdateContext(r.Context(), projectID(r), in)
	if err != nil {
		writeInternalError(r, w, "UPDATE_FAILED", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// DELETE /v1/context/:id
func (h *Handlers) DeleteContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	result, err := h.tools.DeleteContext(r.Context(), projectID(r), id)
	if err != nil {
		writeInternalError(r, w, "DELETE_FAILED", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /v1/context/:id/review
func (h *Handlers) ReviewContext(w http.ResponseWriter, r *http.Request) {
	chunkID := chi.URLParam(r, "id")
	var in mcp.ReviewContextInput
	if err := decodeJSONBody(r, &in); err != nil {
		writeJSONDecodeError(w, err, "")
		return
	}
	in.ChunkID = chunkID
	result, err := h.tools.ReviewContext(r.Context(), projectID(r), in)
	if err != nil {
		writeInternalError(r, w, "REVIEW_FAILED", err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// POST /v1/keys — bootstrap: create org + user + API key
func (h *Handlers) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrgSlug string `json:"org_slug"`
		KeyName string `json:"name"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "org_slug is required")
		return
	}
	if body.OrgSlug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "org_slug is required")
		return
	}
	if body.KeyName == "" {
		body.KeyName = "default"
	}

	plaintext, keyHash, err := auth.GenerateAPIKey(body.OrgSlug)
	if err != nil {
		writeInternalError(r, w, "KEYGEN_FAILED", err)
		return
	}

	ctx := r.Context()

	// Get or create org
	var org models.Org
	err = h.pool.QueryRow(ctx, `
		INSERT INTO orgs (name, slug) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, name, slug
	`, body.OrgSlug, body.OrgSlug).Scan(&org.ID, &org.Name, &org.Slug)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	// Get or create bot user for this org
	botEmail := "agent-" + body.OrgSlug + "@hizal.local"
	var user models.User
	err = h.pool.QueryRow(ctx, `
		INSERT INTO users (email, name) VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, email, name
	`, botEmail, "Agent Bot ("+body.OrgSlug+")").Scan(&user.ID, &user.Email, &user.Name)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	// Create org membership (idempotent)
	_, _ = h.pool.Exec(ctx, `
		INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1, $2, 'admin')
		ON CONFLICT (user_id, org_id) DO NOTHING
	`, user.ID, org.ID)

	// Create default project
	var project models.Project
	err = h.pool.QueryRow(ctx, `
		INSERT INTO projects (org_id, name, slug) VALUES ($1, 'Default', 'default')
		ON CONFLICT (org_id, slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, org_id, name, slug
	`, org.ID).Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	// Create API key
	var key models.APIKey
	err = h.pool.QueryRow(ctx, `
		INSERT INTO api_keys (owner_type, user_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ($1, $2, $3, $4, $5, true, '{}')
		RETURNING id
	`, "USER", user.ID, org.ID, keyHash, body.KeyName).Scan(&key.ID)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         key.ID,
		"key":        plaintext,
		"org_id":     org.ID,
		"project_id": project.ID,
		"note":       "Store this key securely — it will not be shown again.",
	})
}
