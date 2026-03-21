package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/XferOps/winnow/internal/embeddings"
	"github.com/go-chi/chi/v5"
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
		var tagsBytes []byte
		if err := rows.Scan(
			&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
			&tagsBytes, &chunk.OrgName, &chunk.OrgSlug,
			&chunk.CreatedAt, &chunk.UpdatedAt, &totalCount,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "SCAN_FAILED", err.Error())
			return
		}
		json.Unmarshal(contentBytes, &chunk.Content)
		json.Unmarshal(tagsBytes, &chunk.Tags)
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
	var tagsBytes []byte

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
		&tagsBytes, &chunk.OrgName, &chunk.OrgSlug,
		&chunk.CreatedAt, &chunk.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chunk not found or not public")
		return
	}

	json.Unmarshal(contentBytes, &chunk.Content)
	json.Unmarshal(tagsBytes, &chunk.Tags)

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
					var tagsBytes []byte
					if err := rows.Scan(
						&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
						&tagsBytes, &chunk.OrgName, &chunk.OrgSlug,
						&chunk.CreatedAt, &chunk.UpdatedAt,
					); err == nil {
						json.Unmarshal(contentBytes, &chunk.Content)
						json.Unmarshal(tagsBytes, &chunk.Tags)
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
			var tagsBytes []byte
			if err := rows.Scan(
				&chunk.ID, &chunk.Title, &contentBytes, &chunk.ChunkType, &chunk.QueryKey,
				&tagsBytes, &chunk.OrgName, &chunk.OrgSlug,
				&chunk.CreatedAt, &chunk.UpdatedAt,
			); err == nil {
				json.Unmarshal(contentBytes, &chunk.Content)
				json.Unmarshal(tagsBytes, &chunk.Tags)
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
