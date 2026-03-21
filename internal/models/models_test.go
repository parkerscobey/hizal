package models

import (
	"reflect"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
)

func TestCanonicalDBModelsExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		model         any
		dbFields      []string
		pointerFields []string
	}{
		{
			name:  "orgs",
			model: Org{},
			dbFields: []string{
				"id", "name", "slug", "tier", "created_at", "updated_at",
			},
		},
		{
			name:  "users",
			model: User{},
			dbFields: []string{
				"id", "email", "name", "password_hash", "created_at", "updated_at",
			},
			pointerFields: []string{"PasswordHash"},
		},
		{
			name:  "org_memberships",
			model: OrgMembership{},
			dbFields: []string{
				"id", "user_id", "org_id", "role", "created_at",
			},
		},
		{
			name:  "projects",
			model: Project{},
			dbFields: []string{
				"id", "org_id", "name", "slug", "description", "created_at", "updated_at",
			},
			pointerFields: []string{"Description"},
		},
		{
			name:  "project_memberships",
			model: ProjectMembership{},
			dbFields: []string{
				"id", "user_id", "project_id", "role", "created_at",
			},
		},
		{
			name:  "agents",
			model: Agent{},
			dbFields: []string{
				"id", "org_id", "owner_id", "name", "slug", "type", "description", "status",
				"platform", "instance_id", "ip_address", "last_active_at", "created_at", "updated_at", "tags",
			},
			pointerFields: []string{"Description", "Platform", "InstanceID", "IPAddress", "LastActiveAt"},
		},
		{
			name:  "agent_projects",
			model: AgentProject{},
			dbFields: []string{
				"agent_id", "project_id",
			},
		},
		{
			name:  "api_keys",
			model: APIKey{},
			dbFields: []string{
				"id", "user_id", "key_hash", "name", "scope_all_projects", "allowed_project_ids",
				"permissions", "created_at", "updated_at", "last_used_at", "owner_type", "agent_id", "org_id",
			},
			pointerFields: []string{"UserID", "LastUsedAt", "AgentID", "OrgID"},
		},
		{
			name:  "context_chunks",
			model: ContextChunk{},
			dbFields: []string{
				"id", "project_id", "query_key", "title", "content", "embedding", "source_file",
				"source_lines", "gotchas", "related", "created_by_agent", "created_at", "updated_at",
			},
			pointerFields: []string{"SourceFile", "CreatedByAgent"},
		},
		{
			name:  "context_versions",
			model: ContextVersion{},
			dbFields: []string{
				"id", "chunk_id", "version", "content", "change_note", "compacted_from", "created_at",
			},
			pointerFields: []string{"ChangeNote"},
		},
		{
			name:  "context_reviews",
			model: ContextReview{},
			dbFields: []string{
				"id", "chunk_id", "task", "usefulness", "usefulness_note", "correctness", "correctness_note", "action", "created_at",
			},
			pointerFields: []string{"Task", "Usefulness", "UsefulnessNote", "Correctness", "CorrectnessNote", "Action"},
		},
		{
			name:  "usage_snapshots",
			model: UsageSnapshot{},
			dbFields: []string{
				"id", "org_id", "project_id", "date", "api_calls", "chunks_created", "chunks_read",
				"versions_created", "reviews_submitted", "created_at", "updated_at",
			},
			pointerFields: []string{"ProjectID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ := reflect.TypeOf(tt.model)
			if typ.Kind() != reflect.Struct {
				t.Fatalf("model %s must be a struct, got %s", tt.name, typ.Kind())
			}

			fieldsByTag := map[string]reflect.StructField{}
			fieldsByName := map[string]reflect.StructField{}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if dbTag := field.Tag.Get("db"); dbTag != "" {
					fieldsByTag[dbTag] = field
				}
				fieldsByName[field.Name] = field
			}

			for _, dbField := range tt.dbFields {
				if _, ok := fieldsByTag[dbField]; !ok {
					t.Fatalf("%s is missing db field %q", typ.Name(), dbField)
				}
			}

			for _, fieldName := range tt.pointerFields {
				field, ok := fieldsByName[fieldName]
				if !ok {
					t.Fatalf("%s is missing field %q", typ.Name(), fieldName)
				}
				if field.Type.Kind() != reflect.Ptr {
					t.Fatalf("%s.%s should be a pointer, got %s", typ.Name(), fieldName, field.Type)
				}
			}
		})
	}
}

func TestCanonicalDBModelCoreFieldTypes(t *testing.T) {
	t.Parallel()

	timeType := reflect.TypeOf(time.Time{})
	vectorType := reflect.TypeOf(pgvector.Vector{})

	assertFieldType(t, reflect.TypeOf(Org{}), "CreatedAt", timeType)
	assertFieldType(t, reflect.TypeOf(User{}), "CreatedAt", timeType)
	assertFieldType(t, reflect.TypeOf(Project{}), "CreatedAt", timeType)
	assertFieldType(t, reflect.TypeOf(Agent{}), "LastActiveAt", reflect.PtrTo(timeType))
	assertFieldType(t, reflect.TypeOf(ContextChunk{}), "Embedding", vectorType)
	assertFieldType(t, reflect.TypeOf(UsageSnapshot{}), "Date", timeType)
}

func assertFieldType(t *testing.T, typ reflect.Type, fieldName string, want reflect.Type) {
	t.Helper()

	field, ok := typ.FieldByName(fieldName)
	if !ok {
		t.Fatalf("%s is missing field %q", typ.Name(), fieldName)
	}
	if field.Type != want {
		t.Fatalf("%s.%s has type %s, want %s", typ.Name(), fieldName, field.Type, want)
	}
}
