package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/XferOps/winnow/internal/embeddings"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type PublicHandlers struct {
	pool  *pgxpool.Pool
	embed *embeddings.Client
}

func NewPublicHandlers(pool *pgxpool.Pool, embed *embeddings.Client) *PublicHandlers {
	return &PublicHandlers{pool: pool, embed: embed}
}

type PublicChunkResponse struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	ChunkType string    `json:"chunk_type"`
	QueryKey  string    `json:"query_key"`
	Tags      []string  `json:"tags"`
	OrgName   string    `json:"org_name"`
	OrgSlug   string    `json:"org_slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PublicChunkListResponse struct {
	Chunks   []PublicChunkResponse `json:"chunks"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
	HasMore  bool                  `json:"has_more"`
}

func (h *PublicHandlers) ListPublicChunks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page := 1
	if p := q.Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}

	pageSize := 20
	if ps := q.Get("page_size"); ps != "" {
		if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
			if parsed > 100 {
				parsed = 100
			}
			pageSize = parsed
		}
	}

	chunkType := q.Get("type")
	tag := q.Get("tag")
	org := q.Get("org")

	offset := (page - 1) * pageSize

	var chunkTypeParam, tagParam, orgParam interface{}
	if chunkType != "" {
		chunkTypeParam = chunkType
	}
	if tag != "" {
		tagParam = tag
	}
	if org != "" {
		orgParam = org
	}

	ctx := r.Context()
	rows, err := h.pool.Query(ctx, `
		SELECT
			cc.id,
			cc.title,
			cc.content,
			cc.chunk_type,
			cc.query_key,
			COALESCE(cc.tags, '{}') as tags,
			o.name as org_name,
			o.slug as org_slug,
			cc.created_at,
			cc.updated_at,
			COUNT(*) OVER() as total_count
		FROM context_chunks cc
		JOIN orgs o ON o.id = cc.org_id
		WHERE cc.visibility = 'public'
		  AND ($1::text IS NULL OR cc.chunk_type = $1)
		  AND ($2::text IS NULL OR cc.tags @> ARRAY[$2::text])
		  AND ($3::text IS NULL OR LOWER(o.slug) = LOWER($3) OR LOWER(o.name) ILIKE '%' || LOWER($3) || '%')
		ORDER BY cc.updated_at DESC
		LIMIT $4 OFFSET $5
	`, chunkTypeParam, tagParam, orgParam, pageSize, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error())
		return
	}
	defer rows.Close()

	chunks := []PublicChunkResponse{}
	var totalCount int
	for rows.Next() {
		var chunk PublicChunkResponse
		var contentBytes []byte
		if err := rows.Scan(
			&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
			&chunk.Tags, &chunk.OrgName, &chunk.OrgSlug,
			&chunk.CreatedAt, &chunk.UpdatedAt, &totalCount,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "SCAN_FAILED", err.Error())
			return
		}
		json.Unmarshal(contentBytes, &chunk.Content)
		chunks = append(chunks, chunk)
	}

	if chunks == nil {
		chunks = []PublicChunkResponse{}
	}

	hasMore := (page * pageSize) < totalCount

	writeJSON(w, http.StatusOK, PublicChunkListResponse{
		Chunks:   chunks,
		Total:    totalCount,
		Page:     page,
		PageSize: pageSize,
		HasMore:  hasMore,
	})
}

func (h *PublicHandlers) GetPublicChunk(w http.ResponseWriter, r *http.Request) {
	chunkID := chi.URLParam(r, "chunkID")
	if chunkID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "chunk ID is required")
		return
	}

	ctx := r.Context()
	var chunk PublicChunkResponse
	var contentBytes []byte

	err := h.pool.QueryRow(ctx, `
		SELECT
			cc.id, cc.title, cc.content, cc.chunk_type, cc.query_key,
			COALESCE(cc.tags, '{}') as tags,
			o.name as org_name, o.slug as org_slug,
			cc.created_at, cc.updated_at
		FROM context_chunks cc
		JOIN orgs o ON o.id = cc.org_id
		WHERE cc.id = $1 AND cc.visibility = 'public'
	`, chunkID).Scan(
		&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
		&chunk.Tags, &chunk.OrgName, &chunk.OrgSlug,
		&chunk.CreatedAt, &chunk.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chunk not found or not public")
		return
	}

	json.Unmarshal(contentBytes, &chunk.Content)

	writeJSON(w, http.StatusOK, chunk)
}

func (h *PublicHandlers) SearchPublicChunks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := q.Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "q parameter is required")
		return
	}

	page := 1
	if p := q.Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}

	pageSize := 20
	if ps := q.Get("page_size"); ps != "" {
		if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
			if parsed > 100 {
				parsed = 100
			}
			pageSize = parsed
		}
	}

	chunkType := q.Get("type")
	var chunkTypeParam interface{}
	if chunkType != "" {
		chunkTypeParam = chunkType
	}

	offset := (page - 1) * pageSize
	ctx := r.Context()

	var chunks []PublicChunkResponse
	var totalCount int

	if h.embed != nil {
		emb, err := h.embed.Embed(ctx, query)
		if err == nil {
			vec := pgvector.NewVector(emb)
			rows, err := h.pool.Query(ctx, `
				SELECT
					cc.id, cc.title, cc.content, cc.chunk_type, cc.query_key,
					COALESCE(cc.tags, '{}') as tags,
					o.name as org_name, o.slug as org_slug,
					cc.created_at, cc.updated_at
				FROM context_chunks cc
				JOIN orgs o ON o.id = cc.org_id
				WHERE cc.visibility = 'public'
				  AND cc.embedding IS NOT NULL
				  AND ($2::text IS NULL OR cc.chunk_type = $2)
				ORDER BY cc.embedding <=> $1::vector
				LIMIT $3 OFFSET $4
			`, vec, chunkTypeParam, pageSize, offset)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var chunk PublicChunkResponse
					var contentBytes []byte
					if err := rows.Scan(
						&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
						&chunk.Tags, &chunk.OrgName, &chunk.OrgSlug,
						&chunk.CreatedAt, &chunk.UpdatedAt,
					); err == nil {
						json.Unmarshal(contentBytes, &chunk.Content)
						chunks = append(chunks, chunk)
					}
				}
			}
		}
	}

	if chunks == nil {
		likePattern := "%" + query + "%"
		rows, err := h.pool.Query(ctx, `
			SELECT
				cc.id, cc.title, cc.content, cc.chunk_type, cc.query_key,
				COALESCE(cc.tags, '{}') as tags,
				o.name as org_name, o.slug as org_slug,
				cc.created_at, cc.updated_at
			FROM context_chunks cc
			JOIN orgs o ON o.id = cc.org_id
			WHERE cc.visibility = 'public'
			  AND (cc.title ILIKE $1 OR cc.content::text ILIKE $1)
			  AND ($2::text IS NULL OR cc.chunk_type = $2)
			ORDER BY cc.updated_at DESC
			LIMIT $3 OFFSET $4
		`, likePattern, chunkTypeParam, pageSize, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error())
			return
		}
		defer rows.Close()

		for rows.Next() {
			var chunk PublicChunkResponse
			var contentBytes []byte
			if err := rows.Scan(
				&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
				&chunk.Tags, &chunk.OrgName, &chunk.OrgSlug,
				&chunk.CreatedAt, &chunk.UpdatedAt,
			); err == nil {
				json.Unmarshal(contentBytes, &chunk.Content)
				chunks = append(chunks, chunk)
			}
		}
	}

	if chunks == nil {
		chunks = []PublicChunkResponse{}
	}

	totalCount = len(chunks)
	hasMore := (page * pageSize) < totalCount

	writeJSON(w, http.StatusOK, PublicChunkListResponse{
		Chunks:   chunks,
		Total:    totalCount,
		Page:     page,
		PageSize: pageSize,
		HasMore:  hasMore,
	})
}

type AddPublicChunkRequest struct {
	Scope     string  `json:"scope"`
	ProjectID *string `json:"project_id,omitempty"`
	OrgID     *string `json:"org_id,omitempty"`
	AgentID   *string `json:"agent_id,omitempty"`
}

func (h *PublicHandlers) AddPublicChunk(w http.ResponseWriter, r *http.Request) {
	chunkID := chi.URLParam(r, "chunkID")
	if chunkID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "chunk ID is required")
		return
	}

	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	var body AddPublicChunkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if body.Scope == "" {
		writeError(w, http.StatusBadRequest, "MISSING_SCOPE", "scope is required")
		return
	}

	if body.Scope != "PROJECT" && body.Scope != "ORG" && body.Scope != "AGENT" {
		writeError(w, http.StatusBadRequest, "INVALID_SCOPE", "scope must be PROJECT, ORG, or AGENT")
		return
	}

	if body.Scope == "PROJECT" && (body.ProjectID == nil || *body.ProjectID == "") {
		writeError(w, http.StatusBadRequest, "MISSING_PROJECT_ID", "project_id is required when scope is PROJECT")
		return
	}

	if (body.Scope == "ORG" || body.Scope == "AGENT") && (body.OrgID == nil || *body.OrgID == "") {
		writeError(w, http.StatusBadRequest, "MISSING_ORG_ID", "org_id is required when scope is ORG or AGENT")
		return
	}

	if body.Scope == "AGENT" && (body.AgentID == nil || *body.AgentID == "") {
		writeError(w, http.StatusBadRequest, "MISSING_AGENT_ID", "agent_id is required when scope is AGENT")
		return
	}

	ctx := r.Context()

	var sourceTitle, sourceContent, sourceChunkType, sourceQueryKey string
	var sourceTags []string
	var sourceSourceLines, sourceGotchas, sourceRelated []byte
	var sourceProjectID, sourceAgentID, sourceCreatedByAgent *string
	var sourceInjectAudience []byte
	var sourceScope, sourceVisibility, sourceOrgName string

	err := h.pool.QueryRow(ctx, `
		SELECT cc.project_id, cc.scope, cc.agent_id, cc.inject_audience,
		       cc.visibility, cc.chunk_type, cc.query_key, cc.title, cc.content,
		       cc.tags, cc.source_lines, cc.gotchas, cc.related, cc.created_by_agent,
		       o.name as source_org_name
		FROM context_chunks cc
		JOIN orgs o ON o.id = cc.org_id
		WHERE cc.id = $1 AND cc.visibility = 'public'
	`, chunkID).Scan(
		&sourceProjectID, &sourceScope, &sourceAgentID,
		&sourceInjectAudience, &sourceVisibility, &sourceChunkType, &sourceQueryKey,
		&sourceTitle, &sourceContent, &sourceTags, &sourceSourceLines,
		&sourceGotchas, &sourceRelated, &sourceCreatedByAgent,
		&sourceOrgName,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chunk not found or not public")
		return
	}

	var destOrgID string
	switch body.Scope {
	case "PROJECT":
		err := h.pool.QueryRow(ctx, `
			SELECT p.org_id FROM projects p
			LEFT JOIN project_memberships pm ON pm.project_id = p.id AND pm.user_id = $2
			LEFT JOIN org_memberships om ON om.org_id = p.org_id AND om.user_id = $2
			WHERE p.id = $1 AND (pm.user_id IS NOT NULL OR om.user_id IS NOT NULL)
		`, *body.ProjectID, user.ID).Scan(&destOrgID)
		if err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "not authorized to add to this project")
			return
		}

	case "ORG":
		_, err := requireOrgRole(r, h.pool, *body.OrgID, "owner", "admin")
		if err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "must be org owner or admin to add chunks at org scope")
			return
		}
		destOrgID = *body.OrgID

	case "AGENT":
		err := h.pool.QueryRow(ctx, `
			SELECT a.org_id FROM agents a
			LEFT JOIN org_memberships om ON om.org_id = a.org_id AND om.user_id = $2
			WHERE a.id = $1 AND (a.owner_id = $2 OR om.role IN ('owner', 'admin'))
		`, *body.AgentID, user.ID).Scan(&destOrgID)
		if err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "not authorized to add chunks to this agent")
			return
		}
	}

	if body.Scope == "PROJECT" && body.OrgID != nil {
		destOrgID = *body.OrgID
	}

	newID := uuid.New().String()
	now := time.Now()

	var projectID, agentID, orgID *string
	switch body.Scope {
	case "PROJECT":
		projectID = body.ProjectID
		orgID = &destOrgID
	case "ORG":
		orgID = &destOrgID
	case "AGENT":
		agentID = body.AgentID
		orgID = &destOrgID
	}

	sourceChunkID := chunkID
	sourceOrg := sourceOrgName

	_, err = h.pool.Exec(ctx, `
		INSERT INTO context_chunks (
			id, project_id, scope, agent_id, org_id, inject_audience,
			visibility, chunk_type, query_key, title, content,
			tags, source_lines, gotchas, related, created_by_agent,
			source_chunk_id, source_org_name,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			'private', $7, $8, $9, $10,
			$11, $12, $13, $14, $15,
			$16, $17,
			$18, $18
		)
	`, newID, projectID, body.Scope, agentID, orgID, nil,
		sourceChunkType, sourceQueryKey, sourceTitle, sourceContent,
		sourceTags, sourceSourceLines, sourceGotchas, sourceRelated, sourceCreatedByAgent,
		&sourceChunkID, &sourceOrg,
		now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id": newID,
	})
}
