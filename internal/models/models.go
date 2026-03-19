package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// Org represents a row in the orgs table.
type Org struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Slug      string    `json:"slug" db:"slug"`
	Tier      string    `json:"tier" db:"tier"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// User represents a row in the users table.
type User struct {
	ID           string    `json:"id" db:"id"`
	Email        string    `json:"email" db:"email"`
	Name         string    `json:"name" db:"name"`
	PasswordHash *string   `json:"-" db:"password_hash"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// OrgMembership represents a row in the org_memberships table.
type OrgMembership struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	OrgID     string    `json:"org_id" db:"org_id"`
	Role      string    `json:"role" db:"role"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Project represents a row in the projects table.
type Project struct {
	ID          string    `json:"id" db:"id"`
	OrgID       string    `json:"org_id" db:"org_id"`
	Name        string    `json:"name" db:"name"`
	Slug        string    `json:"slug" db:"slug"`
	Description *string   `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// ProjectMembership represents a row in the project_memberships table.
type ProjectMembership struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	ProjectID string    `json:"project_id" db:"project_id"`
	Role      string    `json:"role" db:"role"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Agent represents a row in the agents table.
type Agent struct {
	ID            string     `json:"id" db:"id"`
	OrgID         string     `json:"org_id" db:"org_id"`
	OwnerID       string     `json:"owner_id" db:"owner_id"`
	Name          string     `json:"name" db:"name"`
	Slug          string     `json:"slug" db:"slug"`
	Type          string     `json:"type" db:"type"`
	Description   *string    `json:"description,omitempty" db:"description"`
	Status        string     `json:"status" db:"status"`
	Platform      *string    `json:"platform,omitempty" db:"platform"`
	InstanceID    *string    `json:"instance_id,omitempty" db:"instance_id"`
	IPAddress     *string    `json:"ip_address,omitempty" db:"ip_address"`
	LastActiveAt  *time.Time `json:"last_active_at,omitempty" db:"last_active_at"`
	// MemoryEnabled controls whether this agent can read/write AGENT-scoped chunks.
	// false (default): knowledge-only — PROJECT + ORG scope only.
	// true:            full behavior-driven — AGENT scope unlocked (Pro tier).
	MemoryEnabled bool       `json:"memory_enabled" db:"memory_enabled"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// AgentProject represents a row in the agent_projects table.
type AgentProject struct {
	AgentID   string `json:"agent_id" db:"agent_id"`
	ProjectID string `json:"project_id" db:"project_id"`
}

// APIKey represents a row in the api_keys table.
type APIKey struct {
	ID                string     `json:"id" db:"id"`
	UserID            *string    `json:"user_id,omitempty" db:"user_id"`
	KeyHash           string     `json:"-" db:"key_hash"`
	Name              string     `json:"name" db:"name"`
	ScopeAllProjects  bool       `json:"scope_all_projects" db:"scope_all_projects"`
	AllowedProjectIDs []string   `json:"allowed_project_ids" db:"allowed_project_ids"`
	Permissions       []byte     `json:"permissions" db:"permissions"` // JSONB
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	OwnerType         string     `json:"owner_type" db:"owner_type"`
	AgentID           *string    `json:"agent_id,omitempty" db:"agent_id"`
	OrgID             *string    `json:"org_id,omitempty" db:"org_id"`
}

// ContextChunk represents a row in the context_chunks table.
//
// Scope model:
//   PROJECT (default): shared project knowledge — project_id required.
//   AGENT:             private agent memory — agent_id required, project_id optional.
//   ORG:               org-wide memory — org_id required, project_id NULL.
//
// AlwaysInject:
//   false: retrieved on demand via semantic search.
//   true:  surfaced automatically as ambient baseline layer.
//
// ChunkType:
//   KNOWLEDGE: facts, architecture, conventions (default)
//   RESEARCH:  investigation, findings (most disposable during consolidation)
//   PLAN:      planned work, approaches
//   DECISION:  made decisions, reasoning
type ContextChunk struct {
	ID             string          `json:"id" db:"id"`
	// ProjectID is nullable: NULL for ORG-scoped chunks and cross-project AGENT chunks.
	ProjectID      *string         `json:"project_id,omitempty" db:"project_id"`
	// Scope is PROJECT | AGENT | ORG. Defaults to PROJECT.
	Scope          string          `json:"scope" db:"scope"`
	// AgentID is set for AGENT-scoped chunks.
	AgentID        *string         `json:"agent_id,omitempty" db:"agent_id"`
	// OrgID is set for ORG-scoped chunks.
	OrgID          *string         `json:"org_id,omitempty" db:"org_id"`
	// AlwaysInject: true = ambient baseline, false = on-demand search.
	AlwaysInject   bool            `json:"always_inject" db:"always_inject"`
	// ChunkType: KNOWLEDGE | RESEARCH | PLAN | DECISION. Defaults to KNOWLEDGE.
	ChunkType      string          `json:"chunk_type" db:"chunk_type"`
	QueryKey       string          `json:"query_key" db:"query_key"`
	Title          string          `json:"title" db:"title"`
	Content        []byte          `json:"content" db:"content"` // JSONB
	Embedding      pgvector.Vector `json:"embedding,omitempty" db:"embedding"`
	SourceFile     *string         `json:"source_file,omitempty" db:"source_file"`
	SourceLines    []byte          `json:"source_lines" db:"source_lines"` // JSONB
	Gotchas        []byte          `json:"gotchas" db:"gotchas"`           // JSONB
	Related        []byte          `json:"related" db:"related"`           // JSONB
	CreatedByAgent *string         `json:"created_by_agent,omitempty" db:"created_by_agent"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// ContextVersion represents a row in the context_versions table.
type ContextVersion struct {
	ID            string    `json:"id" db:"id"`
	ChunkID       string    `json:"chunk_id" db:"chunk_id"`
	Version       int       `json:"version" db:"version"`
	Content       []byte    `json:"content" db:"content"` // JSONB
	ChangeNote    *string   `json:"change_note,omitempty" db:"change_note"`
	CompactedFrom []byte    `json:"compacted_from" db:"compacted_from"` // JSONB
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// ContextReview represents a row in the context_reviews table.
type ContextReview struct {
	ID              string    `json:"id" db:"id"`
	ChunkID         string    `json:"chunk_id" db:"chunk_id"`
	Task            *string   `json:"task,omitempty" db:"task"`
	Usefulness      *int      `json:"usefulness,omitempty" db:"usefulness"`
	UsefulnessNote  *string   `json:"usefulness_note,omitempty" db:"usefulness_note"`
	Correctness     *int      `json:"correctness,omitempty" db:"correctness"`
	CorrectnessNote *string   `json:"correctness_note,omitempty" db:"correctness_note"`
	Action          *string   `json:"action,omitempty" db:"action"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
}

// UsageSnapshot represents a row in the usage_snapshots table.
type UsageSnapshot struct {
	ID               string    `json:"id" db:"id"`
	OrgID            string    `json:"org_id" db:"org_id"`
	ProjectID        *string   `json:"project_id,omitempty" db:"project_id"`
	Date             time.Time `json:"date" db:"date"`
	APICalls         int64     `json:"api_calls" db:"api_calls"`
	ChunksCreated    int64     `json:"chunks_created" db:"chunks_created"`
	ChunksRead       int64     `json:"chunks_read" db:"chunks_read"`
	VersionsCreated  int64     `json:"versions_created" db:"versions_created"`
	ReviewsSubmitted int64     `json:"reviews_submitted" db:"reviews_submitted"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// SessionLifecycle represents a row in the session_lifecycles table.
// org_id = NULL means a global built-in preset (default, dev, admin).
type SessionLifecycle struct {
	ID        string    `json:"id" db:"id"`
	OrgID     *string   `json:"org_id,omitempty" db:"org_id"`
	Name      string    `json:"name" db:"name"`
	Slug      string    `json:"slug" db:"slug"`
	IsDefault bool      `json:"is_default" db:"is_default"`
	Config    []byte    `json:"config" db:"config"` // JSONB
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// SessionLifecycleConfig is the parsed form of SessionLifecycle.Config.
type SessionLifecycleConfig struct {
	TTLHours                int      `json:"ttl_hours"`
	RequiredSteps           []string `json:"required_steps"`
	ConsolidationThreshold  int      `json:"consolidation_threshold"`
	InjectScopes            []string `json:"inject_scopes"`
}

// Session represents a row in the sessions table.
// One active session per agent is enforced by a partial unique index.
type Session struct {
	ID                 string     `json:"id" db:"id"`
	AgentID            string     `json:"agent_id" db:"agent_id"`
	ProjectID          *string    `json:"project_id,omitempty" db:"project_id"`
	OrgID              string     `json:"org_id" db:"org_id"`
	LifecycleID        *string    `json:"lifecycle_id,omitempty" db:"lifecycle_id"`
	Status             string     `json:"status" db:"status"` // active | ended | expired
	FocusTask          *string    `json:"focus_task,omitempty" db:"focus_task"`
	ChunksWritten      int        `json:"chunks_written" db:"chunks_written"`
	ChunksRead         int        `json:"chunks_read" db:"chunks_read"`
	ConsolidationDone  bool       `json:"consolidation_done" db:"consolidation_done"`
	ResumeCount        int        `json:"resume_count" db:"resume_count"`
	ExpiresAt          time.Time  `json:"expires_at" db:"expires_at"`
	StartedAt          time.Time  `json:"started_at" db:"started_at"`
	EndedAt            *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}
