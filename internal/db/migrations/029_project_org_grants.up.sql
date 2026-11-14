-- Migration 029: cross-org project sharing via per-project grants (#141)
--
-- Today a project is a single-org island: projects.org_id is NOT NULL and
-- agent_projects links an agent to a project, but agents are org-bound — so an
-- agent whose home org is B cannot be attached to a project owned by org A.
-- This table is the grant that opens that one door, per project.
--
-- PER-PROJECT GRANTS, NOT ORG-TO-ORG LINKS. A link between two orgs would share
-- every future project too — the firewall-rule-as-allowlist problem. A grant is
-- explicit, scoped to ONE project, and revocable; the project stays owned by its
-- home org.
--
-- ROLE GATES WRITE. 'member' = read + write chunks; 'viewer' = read only. No new
-- role surface — these reuse the project-membership vocabulary.
--
-- READ SCOPE IS UNCHANGED AND LOAD-BEARING. This table grants *attachment*, not
-- *visibility*. A cross-org attached agent reads a project's PROJECT-scoped
-- chunks and NOTHING MORE: the home org's ORG-scoped chunks stay invisible
-- (org scope is ownership, not visibility). The invariant, asserted in tests:
-- a cross-org attached agent sees exactly what an in-org attached agent sees.
--
-- REVOCATION FAILS CLOSED. The attach/authorize checks consult this table on
-- every call; deleting a row revokes access on the NEXT tool call, and a
-- revoked foreign agent's session is not silently orphaned (it fails closed).

CREATE TABLE IF NOT EXISTS project_org_grants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    role       VARCHAR(20) NOT NULL,
    granted_by UUID,  -- the user/agent that created the grant (nullable: system/backfill)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT project_org_grants_role_check CHECK (role IN ('member', 'viewer')),
    -- One grant per (project, org): re-granting updates the role rather than
    -- stacking rows, so revocation is a single DELETE.
    CONSTRAINT project_org_grants_project_org_key UNIQUE (project_id, org_id)
);

CREATE INDEX IF NOT EXISTS project_org_grants_org_idx ON project_org_grants(org_id);
CREATE INDEX IF NOT EXISTS project_org_grants_project_idx ON project_org_grants(project_id);
