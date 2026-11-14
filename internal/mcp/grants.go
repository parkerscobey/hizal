package mcp

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Cross-org project sharing (#141).
//
// A project is owned by its home org (projects.org_id). An agent whose home org
// differs may be attached to that project iff its home org holds a grant on it
// (project_org_grants). This file is the ONE place that answers "may this org
// reach this project, and with what role?" — the API attach guard and the MCP
// session/read paths both call it, so the rule cannot drift between them.
//
// READ SCOPE IS NOT THIS FUNCTION'S JOB. A grant authorizes *attachment*; what a
// cross-org attached agent may then READ is the scope filter's job, and it
// already excludes the home org's ORG-scoped chunks (org scope is ownership, not
// visibility). The invariant — a cross-org attached agent sees exactly what an
// in-org attached agent sees — is asserted in tests, not re-derived here.

// ProjectGrantRole is the effective role an org holds on a project.
type ProjectGrantRole string

const (
	// GrantNone: the org holds no grant and is not the project's home org.
	GrantNone ProjectGrantRole = ""
	// GrantViewer: read access to the project's chunks, no writes.
	GrantViewer ProjectGrantRole = "viewer"
	// GrantMember: read and write access to the project's chunks.
	GrantMember ProjectGrantRole = "member"
)

// projectReachableByOrg reports whether orgID may reach projectID, and with what
// role. The project's HOME org always has full (member) access — that is the
// pre-existing behaviour, unchanged. A foreign org reaches the project only via
// a live grant in project_org_grants; deleting that row revokes access on the
// NEXT call, which is what makes revocation fail closed rather than merely stop
// new attaches.
//
// This runs on every attach/session/authorize call — it is a live check, not a
// cached decision, precisely so revocation takes effect immediately.
func projectReachableByOrg(ctx context.Context, pool *pgxpool.Pool, orgID, projectID string) (ProjectGrantRole, error) {
	var homeOrg string
	if err := pool.QueryRow(ctx,
		`SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&homeOrg); err != nil {
		// Unknown project: not reachable. The caller turns this into a
		// resolve-or-fail message naming list_projects.
		return GrantNone, nil
	}
	if homeOrg == orgID {
		return GrantMember, nil // home org: full access, as before
	}
	var role string
	err := pool.QueryRow(ctx,
		`SELECT role FROM project_org_grants WHERE project_id = $1 AND org_id = $2`,
		projectID, orgID).Scan(&role)
	if err != nil {
		return GrantNone, nil // no grant row: no access
	}
	return ProjectGrantRole(role), nil
}

// OrgHoldsGrantForProject is the boolean form used where only reachability
// matters (not the role). Exported for the REST attach guard, which asks the
// same question the MCP session path asks.
func OrgHoldsGrantForProject(ctx context.Context, pool *pgxpool.Pool, orgID, projectID string) (bool, error) {
	role, err := projectReachableByOrg(ctx, pool, orgID, projectID)
	return role != GrantNone, err
}

// ProjectHomeOrg returns the org that owns the project (projects.org_id).
// Exported for the REST layer, which needs the home org to tell the owner's org
// apart from a grantee org.
func ProjectHomeOrg(ctx context.Context, pool *pgxpool.Pool, projectID string) (string, error) {
	var homeOrg string
	err := pool.QueryRow(ctx,
		`SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&homeOrg)
	return homeOrg, err
}

// AgentHomeOrg returns the org an agent belongs to (agents.org_id).
func AgentHomeOrg(ctx context.Context, pool *pgxpool.Pool, agentID string) (string, error) {
	var orgID string
	err := pool.QueryRow(ctx,
		`SELECT org_id FROM agents WHERE id = $1`, agentID).Scan(&orgID)
	return orgID, err
}

// RecordCrossOrgWrite logs an attributable audit row when an agent whose home
// org differs from the project's home org writes to that project (#141,
// acceptance 2: "record the foreign writer … an audit row with the agent's home
// org"). A cross-org write with no record is how a shared project becomes an
// unaccountable one; this makes it attributable without stamping a column onto
// every context_chunks insert.
//
// actorID is the writing agent (audit_log.actor_id is NOT NULL). Best-effort:
// recording must not fail the write itself — the write already happened.
// Returns the foreign org when one was recorded, "" when not (in-org write, or
// no project).
func RecordCrossOrgWrite(ctx context.Context, pool *pgxpool.Pool, writerOrg, actorID, projectID, resourceID string) string {
	if writerOrg == "" || projectID == "" {
		return ""
	}
	homeOrg, err := ProjectHomeOrg(ctx, pool, projectID)
	if err != nil || homeOrg == "" || homeOrg == writerOrg {
		return "" // in-org write (or unknown project): nothing foreign to record
	}
	meta, _ := json.Marshal(map[string]string{"writer_org": writerOrg})
	_, _ = pool.Exec(ctx, `
		INSERT INTO audit_log (org_id, actor_type, actor_id, action, resource_type, resource_id, metadata)
		VALUES ($1, 'AGENT', $2, 'cross_org_chunk_write', 'context_chunk', $3, $4::jsonb)
	`, homeOrg, nullIfEmpty(actorID), resourceID, string(meta))
	return writerOrg
}

// nullIfEmpty returns nil for "" so a NULLIF-style optional uuid column stays
// NULL rather than erroring on an empty string.
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
