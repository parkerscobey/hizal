package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// Org represents an organization.
type Org struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Slug      string    `json:"slug" db:"slug"`
	Tier      string    `json:"tier" db:"tier"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// User represents a user account.
type User struct {
	ID        string    `json:"id" db:"id"`
	Email     string    `json:"email" db:"email"`
	Name      string    `json:"name" db:"name"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// OrgMembership links a user to an org with a role.
type OrgMembership struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	OrgID     string    `json:"org_id" db:"org_id"`
	Role      string    `json:"role" db:"role"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Project represents a project within an org.
type Project struct {
	ID        string    `json:"id" db:"id"`
	OrgID     string    `json:"org_id" db:"org_id"`
	Name      string    `json:"name" db:"name"`
	Slug      string    `json:"slug" db:"slug"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// ProjectMembership links a user to a project with a role.
type ProjectMembership struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	ProjectID string    `json:"project_id" db:"project_id"`
	Role      string    `json:"role" db:"role"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// APIKey represents an API key for accessing the Winnow API.
type APIKey struct {
	ID                 string     `json:"id" db:"id"`
	UserID             string     `json:"user_id" db:"user_id"`
	KeyHash            string     `json:"-" db:"key_hash"`
	Name               string     `json:"name" db:"name"`
	ScopeAllProjects   bool       `json:"scope_all_projects" db:"scope_all_projects"`
	AllowedProjectIDs  []string   `json:"allowed_project_ids" db:"allowed_project_ids"`
	Permissions        []byte     `json:"permissions" db:"permissions"` // JSONB
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
}

// ContextChunk is the core unit of stored context for AI coding agents.
type ContextChunk struct {
	ID              string         `json:"id" db:"id"`
	ProjectID       string         `json:"project_id" db:"project_id"`
	QueryKey        string         `json:"query_key" db:"query_key"`
	Title           string         `json:"title" db:"title"`
	Content         []byte         `json:"content" db:"content"`         // JSONB
	Embedding       pgvector.Vector `json:"embedding,omitempty" db:"embedding"`
	SourceFile      string         `json:"source_file,omitempty" db:"source_file"`
	SourceLines     []byte         `json:"source_lines" db:"source_lines"` // JSONB
	Gotchas         []byte         `json:"gotchas" db:"gotchas"`           // JSONB
	Related         []byte         `json:"related" db:"related"`           // JSONB
	CreatedByAgent  string         `json:"created_by_agent,omitempty" db:"created_by_agent"`
	CreatedAt       time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at" db:"updated_at"`
}

// ContextVersion records a historical version of a context chunk.
type ContextVersion struct {
	ID            string    `json:"id" db:"id"`
	ChunkID       string    `json:"chunk_id" db:"chunk_id"`
	Version       int       `json:"version" db:"version"`
	Content       []byte    `json:"content" db:"content"`         // JSONB
	ChangeNote    string    `json:"change_note,omitempty" db:"change_note"`
	CompactedFrom []byte    `json:"compacted_from" db:"compacted_from"` // JSONB
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// ContextReview records agent feedback on a context chunk's quality.
type ContextReview struct {
	ID              string    `json:"id" db:"id"`
	ChunkID         string    `json:"chunk_id" db:"chunk_id"`
	Task            string    `json:"task,omitempty" db:"task"`
	Usefulness      int       `json:"usefulness" db:"usefulness"`
	UsefulnessNote  string    `json:"usefulness_note,omitempty" db:"usefulness_note"`
	Correctness     int       `json:"correctness" db:"correctness"`
	CorrectnessNote string    `json:"correctness_note,omitempty" db:"correctness_note"`
	Action          string    `json:"action,omitempty" db:"action"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
}
