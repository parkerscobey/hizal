package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/XferOps/winnow/internal/mcp"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReviewHandlers struct {
	pool *pgxpool.Pool
}

func NewReviewHandlers(pool *pgxpool.Pool) *ReviewHandlers {
	return &ReviewHandlers{pool: pool}
}

type ReviewInboxItem struct {
	ID              string        `json:"id"`
	QueryKey        string        `json:"query_key"`
	Title           string        `json:"title"`
	Scope           string        `json:"scope"`
	ChunkType       string        `json:"chunk_type"`
	ProjectID       *string       `json:"project_id"`
	ProjectName     *string       `json:"project_name"`
	LastReviewAt    *time.Time    `json:"last_review_at"`
	DaysSinceReview int           `json:"days_since_review"`
	StaleSignals    []StaleSignal `json:"stale_signals"`
	MinUsefulness   *int          `json:"min_usefulness"`
	MinCorrectness  *int          `json:"min_correctness"`
	Freshness       float64       `json:"freshness"`
}

type StaleSignal struct {
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

type ReviewInboxResponse struct {
	Chunks []ReviewInboxItem `json:"chunks"`
	Total  int               `json:"total"`
}

func (h *ReviewHandlers) ReviewInbox(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	ctx := r.Context()

	rows, err := h.pool.Query(ctx, `
		WITH org_projects AS (
			SELECT id FROM projects WHERE org_id = $1
		),
		org_chunks AS (
			SELECT id, project_id, org_id, query_key, title, scope, chunk_type, updated_at
			FROM context_chunks
			WHERE org_id = $1 OR project_id IN (SELECT id FROM org_projects)
		),
		review_stats AS (
			SELECT
				chunk_id,
				MAX(created_at) AS last_review_at,
				MIN(usefulness) AS min_usefulness,
				MIN(correctness) AS min_correctness,
				BOOL_OR(action IN ('needs_update', 'outdated', 'incorrect')) AS has_explicit_flag,
				BOOL_OR(usefulness < 3 OR correctness < 3) AS has_low_score
			FROM context_reviews
			WHERE chunk_id IN (SELECT id FROM org_chunks)
			GROUP BY chunk_id
		),
		needs_review AS (
			SELECT
				oc.id,
				oc.query_key,
				oc.title,
				oc.scope,
				oc.chunk_type,
				oc.project_id,
				oc.updated_at AS chunk_updated_at,
				COALESCE(rs.last_review_at, oc.updated_at) AS last_activity,
				rs.min_usefulness,
				rs.min_correctness,
				rs.has_explicit_flag,
				rs.has_low_score,
				CASE
					WHEN rs.has_explicit_flag OR rs.has_low_score THEN 0
					WHEN rs.last_review_at IS NULL THEN 1
					WHEN rs.last_review_at < NOW() - INTERVAL '30 days' THEN 2
					ELSE 3
				END AS priority_bucket
			FROM org_chunks oc
			LEFT JOIN review_stats rs ON rs.chunk_id = oc.id
			WHERE
				rs.has_explicit_flag = TRUE
				OR rs.has_low_score = TRUE
				OR rs.last_review_at IS NULL
				OR rs.last_review_at < NOW() - INTERVAL '30 days'
		)
		SELECT
			nr.id,
			nr.query_key,
			nr.title,
			nr.scope,
			nr.chunk_type,
			nr.project_id,
			p.name AS project_name,
			nr.last_activity,
			nr.min_usefulness,
			nr.min_correctness,
			nr.priority_bucket,
			ARRAY(
				SELECT jsonb_build_object(
					'action', cr.action,
					'note', COALESCE(
						NULLIF(cr.usefulness_note, ''),
						NULLIF(cr.correctness_note, '')
					)
				)
				FROM context_reviews cr
				WHERE cr.chunk_id = nr.id
				AND (
					cr.action IN ('needs_update', 'outdated', 'incorrect')
					OR cr.usefulness < 3
					OR cr.correctness < 3
				)
				ORDER BY cr.created_at DESC
			) AS stale_signals_json
		FROM needs_review nr
		LEFT JOIN projects p ON p.id = nr.project_id
		ORDER BY nr.priority_bucket ASC, nr.last_activity ASC
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var items []ReviewInboxItem

	for rows.Next() {
		var (
			id, queryKey, title, scope, chunkType string
			projectID                              *string
			projectName                            *string
			lastActivity                           time.Time
			minUsefulness, minCorrectness          *int
			priorityBucket                         int
			signalsJSON                            [][]byte
		)
		if err := rows.Scan(&id, &queryKey, &title, &scope, &chunkType, &projectID, &projectName, &lastActivity, &minUsefulness, &minCorrectness, &priorityBucket, &signalsJSON); err != nil {
			writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}

		signals := make([]StaleSignal, 0)
		for _, b := range signalsJSON {
			if len(b) == 0 {
				continue
			}
			var s struct {
				Action string `json:"action"`
				Note   string `json:"note"`
			}
			if json.Unmarshal(b, &s) == nil {
				signals = append(signals, StaleSignal{Action: s.Action, Note: s.Note})
			}
		}

		daysSince := int(time.Since(lastActivity).Hours() / 24)
		freshness := 1.0
		if daysSince > int(mcp.FreshnessDecayStartDays) {
			decay := float64(daysSince-int(mcp.FreshnessDecayStartDays)) / (mcp.FreshnessDecayFullDays - mcp.FreshnessDecayStartDays)
			if decay >= 1.0 {
				freshness = mcp.FreshnessMin
			} else {
				freshness = 1.0 - (1.0-mcp.FreshnessMin)*decay
			}
		}

		items = append(items, ReviewInboxItem{
			ID:              id,
			QueryKey:        queryKey,
			Title:           title,
			Scope:           scope,
			ChunkType:       chunkType,
			ProjectID:       projectID,
			ProjectName:     projectName,
			LastReviewAt:    &lastActivity,
			DaysSinceReview: daysSince,
			StaleSignals:    signals,
			MinUsefulness:   minUsefulness,
			MinCorrectness:  minCorrectness,
			Freshness:       freshness,
		})
	}

	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if items == nil {
		items = []ReviewInboxItem{}
	}

	writeJSON(w, http.StatusOK, ReviewInboxResponse{
		Chunks: items,
		Total:  len(items),
	})
}
