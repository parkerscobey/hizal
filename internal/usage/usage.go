// Package usage provides lightweight analytics tracking for Hizal API calls.
// Each request upserts a daily counter row via ON CONFLICT DO UPDATE.
package usage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation constants for usage tracking.
const (
	OpWrite   = "write"
	OpRead    = "read"
	OpSearch  = "search"
	OpUpdate  = "update"
	OpCompact = "compact"
	OpReview  = "review"
	OpDelete  = "delete"
)

// Tracker records API usage into usage_snapshots via daily upsert.
type Tracker struct {
	pool *pgxpool.Pool
}

// New creates a Tracker backed by the given pool.
func New(pool *pgxpool.Pool) *Tracker {
	return &Tracker{pool: pool}
}

// Track records one API call for the given org/project and operation.
// It runs asynchronously so it never blocks the request path.
func (t *Tracker) Track(orgID, projectID, op string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		t.record(ctx, orgID, projectID, op)
	}()
}

func (t *Tracker) record(ctx context.Context, orgID, projectID, op string) {
	today := time.Now().UTC().Format("2006-01-02")

	// Build per-operation increment fragments
	chunkCreated := boolInt(op == OpWrite)
	chunkRead := boolInt(op == OpRead || op == OpSearch || op == OpCompact)
	versionCreated := boolInt(op == OpWrite || op == OpUpdate || op == OpCompact)
	reviewSubmitted := boolInt(op == OpReview)

	// Handle nullable project_id
	var projArg interface{}
	if projectID != "" {
		projArg = projectID
	}

	_, _ = t.pool.Exec(ctx, `
		INSERT INTO usage_snapshots
			(org_id, project_id, date, api_calls, chunks_created, chunks_read, versions_created, reviews_submitted, updated_at)
		VALUES
			($1, $2, $3, 1, $4, $5, $6, $7, NOW())
		ON CONFLICT (org_id, project_id, date) DO UPDATE SET
			api_calls         = usage_snapshots.api_calls         + 1,
			chunks_created    = usage_snapshots.chunks_created    + $4,
			chunks_read       = usage_snapshots.chunks_read       + $5,
			versions_created  = usage_snapshots.versions_created  + $6,
			reviews_submitted = usage_snapshots.reviews_submitted + $7,
			updated_at        = NOW()
	`, orgID, projArg, today, chunkCreated, chunkRead, versionCreated, reviewSubmitted)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DailySnapshot is one row returned from the usage endpoint.
type DailySnapshot struct {
	Date             string  `json:"date"`
	ProjectID        *string `json:"project_id,omitempty"`
	APICalls         int64   `json:"api_calls"`
	ChunksCreated    int64   `json:"chunks_created"`
	ChunksRead       int64   `json:"chunks_read"`
	VersionsCreated  int64   `json:"versions_created"`
	ReviewsSubmitted int64   `json:"reviews_submitted"`
}

// Query returns daily snapshots for an org over the past `days` days.
// If projectID is non-empty, results are filtered to that project.
func Query(ctx context.Context, pool *pgxpool.Pool, orgID string, projectID string, days int) ([]DailySnapshot, error) {
	if days <= 0 || days > 365 {
		days = 30
	}

	var rows interface {
		Next() bool
		Scan(...any) error
		Err() error
		Close()
	}
	var err error

	if projectID != "" {
		rows, err = pool.Query(ctx, `
			SELECT date::text, project_id::text, api_calls, chunks_created, chunks_read, versions_created, reviews_submitted
			FROM usage_snapshots
			WHERE org_id = $1
			  AND project_id = $2
			  AND date >= CURRENT_DATE - ($3 - 1) * INTERVAL '1 day'
			ORDER BY date DESC
		`, orgID, projectID, days)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT date::text, project_id::text, api_calls, chunks_created, chunks_read, versions_created, reviews_submitted
			FROM usage_snapshots
			WHERE org_id = $1
			  AND date >= CURRENT_DATE - ($2 - 1) * INTERVAL '1 day'
			ORDER BY date DESC
		`, orgID, days)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DailySnapshot
	for rows.Next() {
		var s DailySnapshot
		var projID *string
		if err := rows.Scan(&s.Date, &projID, &s.APICalls, &s.ChunksCreated, &s.ChunksRead, &s.VersionsCreated, &s.ReviewsSubmitted); err != nil {
			return nil, err
		}
		s.ProjectID = projID
		result = append(result, s)
	}
	return result, rows.Err()
}
