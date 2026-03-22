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
	ID           string     `json:"id" db:"id"`
	OrgID        string     `json:"org_id" db:"org_id"`
	OwnerID      string     `json:"owner_id" db:"owner_id"`
	Name         string     `json:"name" db:"name"`
	Slug         string     `json:"slug" db:"slug"`
	Type         string     `json:"type" db:"type"`
	Description  *string    `json:"description,omitempty" db:"description"`
	Status       string     `json:"status" db:"status"`
	Platform     *string    `json:"platform,omitempty" db:"platform"`
	InstanceID   *string    `json:"instance_id,omitempty" db:"instance_id"`
	IPAddress    *string    `json:"ip_address,omitempty" db:"ip_address"`
	LastActiveAt *time.Time `json:"last_active_at,omitempty" db:"last_active_at"`
	TypeID                  *string               `json:"type_id,omitempty" db:"type_id"`
	SearchFilterOverrides   AgentTypeFilterConfig `json:"search_filter_overrides" db:"search_filter_overrides"`
	// MemoryEnabled controls whether this agent can read/write AGENT-scoped chunks.
	// false (default): knowledge-only — PROJECT + ORG scope only.
	// true:            full behavior-driven — AGENT scope unlocked (Pro tier).
	MemoryEnabled bool      `json:"memory_enabled" db:"memory_enabled"`
	Tags          []string  `json:"tags" db:"tags"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
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
//	IDENTITY:       agent identity and traits (AGENT scope, never injected)
//	MEMORY:         episodic context (AGENT scope, never injected)
//	KNOWLEDGE:      facts and documentation (PROJECT scope, default)
//	DECISION:       architectural decisions (PROJECT scope)
//	RESEARCH:       research findings (PROJECT scope)
//	PLAN:           plans and roadmaps (PROJECT scope)
//	SPEC:           specs and designs (PROJECT scope)
//	IMPLEMENTATION: implementation notes (PROJECT scope)
//	CONVENTION:     coding standards (PROJECT scope, inject_audience all)
//	PRINCIPLE:      principles (ORG scope, promoted by human, inject_audience all)
//	CONSTRAINT:     hard limits and requirements (PROJECT scope, inject_audience all, KEEP)
//	LESSON:         learned lessons (PROJECT scope, SURFACE)
//	Org-specific types: fully CRUD-able
//	Global types (org_id=NULL): immutable — PATCH/DELETE return 403
type ContextChunk struct {
	ID string `json:"id" db:"id"`
	// ProjectID is nullable: NULL for ORG-scoped chunks and cross-project AGENT chunks.
	ProjectID *string `json:"project_id,omitempty" db:"project_id"`
	// Scope is PROJECT | AGENT | ORG. Defaults to PROJECT.
	Scope string `json:"scope" db:"scope"`
	// AgentID is set for AGENT-scoped chunks.
	AgentID *string `json:"agent_id,omitempty" db:"agent_id"`
	// OrgID is set for ORG-scoped chunks.
	OrgID *string `json:"org_id,omitempty" db:"org_id"`
	// InjectAudience: JSONB targeting spec for ambient injection. NULL = on-demand only.
	InjectAudience *InjectAudience `json:"inject_audience" db:"inject_audience"`
	// Visibility controls public hub discoverability. "private" (default) | "public".
	// Public chunks are discoverable on the hub but never auto-injected.
	Visibility     string          `json:"visibility" db:"visibility"`
	// ChunkType: IDENTITY | MEMORY | KNOWLEDGE | CONVENTION | PRINCIPLE | DECISION | RESEARCH | PLAN | SPEC | IMPLEMENTATION | CONSTRAINT | LESSON. Defaults to KNOWLEDGE.
	ChunkType      string          `json:"chunk_type" db:"chunk_type"`
	QueryKey       string          `json:"query_key" db:"query_key"`
	Title          string          `json:"title" db:"title"`
	Content        []byte          `json:"content" db:"content"` // JSONB
	Embedding      pgvector.Vector `json:"embedding,omitempty" db:"embedding"`
	SourceFile     *string         `json:"source_file,omitempty" db:"source_file"`
	SourceChunkID  *string         `json:"source_chunk_id,omitempty" db:"source_chunk_id"`
	SourceOrgName  *string         `json:"source_org_name,omitempty" db:"source_org_name"`
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

// AgentTypeFilterConfig represents JSONB filters for inject/search behavior.
type AgentTypeFilterConfig struct {
	IncludeScopes                  []string `json:"include_scopes,omitempty"`
	ExcludeScopes                  []string `json:"exclude_scopes,omitempty"`
	IncludeChunkTypes              []string `json:"include_chunk_types,omitempty"`
	ExcludeChunkTypes              []string `json:"exclude_chunk_types,omitempty"`
	ExcludeQueryKeyPrefixes        []string `json:"exclude_query_key_prefixes,omitempty"`
	OrgSearchRequiresExplicitScope bool     `json:"org_search_requires_explicit_scope,omitempty"`
	ExcludeQueryKeys               []string `json:"exclude_query_keys,omitempty"`
	MaxInjectTokens                int      `json:"max_inject_tokens,omitempty"`
}

// AgentType represents a row in the agent_types table.
// org_id = NULL means a global preset.
type AgentType struct {
	ID            string                `json:"id" db:"id"`
	OrgID         *string               `json:"org_id,omitempty" db:"org_id"`
	Name          string                `json:"name" db:"name"`
	Slug          string                `json:"slug" db:"slug"`
	BaseType      *string               `json:"base_type,omitempty" db:"base_type"`
	Description   *string               `json:"description,omitempty" db:"description"`
	InjectFilters AgentTypeFilterConfig `json:"inject_filters" db:"inject_filters"`
	SearchFilters AgentTypeFilterConfig `json:"search_filters" db:"search_filters"`
	CreatedAt     time.Time             `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at" db:"updated_at"`
}

// ChunkType represents a row in the chunk_types table.
// org_id = NULL means a global preset. Global presets are immutable.
//
// The 12 canonical global types are:
//   - IDENTITY:    AGENT scope, inject_audience all, consolidation=KEEP
//   - MEMORY:      AGENT scope, inject_audience nil, consolidation=SURFACE
//   - KNOWLEDGE:   PROJECT scope, inject_audience nil, consolidation=KEEP
//   - CONVENTION:  PROJECT scope, inject_audience all, consolidation=KEEP
//   - PRINCIPLE:   ORG scope, inject_audience all, consolidation=KEEP
//   - DECISION:   PROJECT scope, inject_audience nil, consolidation=KEEP
//   - RESEARCH:    PROJECT scope, inject_audience nil, consolidation=SURFACE
//   - PLAN:        PROJECT scope, inject_audience nil, consolidation=SURFACE
//   - SPEC:        PROJECT scope, inject_audience nil, consolidation=SURFACE
//   - IMPLEMENTATION: PROJECT scope, inject_audience nil, consolidation=SURFACE
//   - CONSTRAINT:  PROJECT scope, inject_audience all, consolidation=KEEP
//   - LESSON:      PROJECT scope, inject_audience nil, consolidation=SURFACE
//
// Org-specific types (org_id != NULL) are fully CRUD-able.
type ChunkType struct {
	ID                    string    `json:"id" db:"id"`
	OrgID                 *string   `json:"org_id,omitempty" db:"org_id"`
	Name                  string    `json:"name" db:"name"`
	Slug                  string    `json:"slug" db:"slug"`
	Description           *string   `json:"description,omitempty" db:"description"`
	DefaultScope          string    `json:"default_scope" db:"default_scope"`
	DefaultInjectAudience *InjectAudience `json:"default_inject_audience" db:"default_inject_audience"`
	ConsolidationBehavior string    `json:"consolidation_behavior" db:"consolidation_behavior"`
	CreatedAt             time.Time `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time `json:"updated_at" db:"updated_at"`
}

// SessionLifecycle represents a row in the session_lifecycles table.
// org_id = NULL means a global built-in preset (default, dev, admin, orchestrator).
type SessionLifecycle struct {
	ID          string    `json:"id" db:"id"`
	OrgID       *string   `json:"org_id,omitempty" db:"org_id"`
	Name        string    `json:"name" db:"name"`
	Slug        string    `json:"slug" db:"slug"`
	IsDefault   bool      `json:"is_default" db:"is_default"`
	Description *string   `json:"description,omitempty" db:"description"`
	Config      []byte    `json:"config" db:"config"` // JSONB
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// SessionLifecycleConfig is the parsed form of SessionLifecycle.Config.
type SessionLifecycleConfig struct {
	TTLHours               int      `json:"ttl_hours"`
	RequiredSteps          []string `json:"required_steps"`
	ConsolidationThreshold int      `json:"consolidation_threshold"`
	InjectScopes           []string `json:"inject_scopes"`
}

// Session represents a row in the sessions table.
// One active session per agent is enforced by a partial unique index.
type Session struct {
	ID                string     `json:"id" db:"id"`
	AgentID           string     `json:"agent_id" db:"agent_id"`
	ProjectID         *string    `json:"project_id,omitempty" db:"project_id"`
	OrgID             string     `json:"org_id" db:"org_id"`
	LifecycleID       *string    `json:"lifecycle_id,omitempty" db:"lifecycle_id"`
	Status            string     `json:"status" db:"status"` // active | ended | expired
	FocusTask         *string    `json:"focus_task,omitempty" db:"focus_task"`
	FocusTags         []string   `json:"focus_tags,omitempty" db:"focus_tags"`
	ChunksWritten     int        `json:"chunks_written" db:"chunks_written"`
	ChunksRead        int        `json:"chunks_read" db:"chunks_read"`
	ConsolidationDone bool       `json:"consolidation_done" db:"consolidation_done"`
	ResumeCount       int        `json:"resume_count" db:"resume_count"`
	ExpiresAt         time.Time  `json:"expires_at" db:"expires_at"`
	StartedAt         time.Time  `json:"started_at" db:"started_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
}
