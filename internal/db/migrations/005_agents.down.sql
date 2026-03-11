DROP INDEX IF EXISTS api_keys_agent_idx;
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_owner_check;
ALTER TABLE api_keys DROP COLUMN IF EXISTS agent_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS owner_type;
ALTER TABLE api_keys ALTER COLUMN user_id SET NOT NULL;

DROP INDEX IF EXISTS agent_projects_project_idx;
DROP TABLE IF EXISTS agent_projects;

DROP INDEX IF EXISTS agents_owner_idx;
DROP INDEX IF EXISTS agents_org_idx;
DROP TABLE IF EXISTS agents;
