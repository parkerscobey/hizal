-- Migration 029 rollback: drop the per-project cross-org grants (#141).

DROP INDEX IF EXISTS project_org_grants_org_idx;
DROP INDEX IF EXISTS project_org_grants_project_idx;
DROP TABLE IF EXISTS project_org_grants;
