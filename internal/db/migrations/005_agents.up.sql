-- WNW-30: Agent model + agent-scoped API keys

-- Agents table
CREATE TABLE IF NOT EXISTS agents (
  id             TEXT        NOT NULL PRIMARY KEY,
  org_id         TEXT        NOT NULL REFERENCES orgs(id)  ON DELETE CASCADE,
  owner_id       TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT        NOT NULL,
  slug           TEXT        NOT NULL,
  type           TEXT        NOT NULL DEFAULT 'CUSTOM',   -- ASSISTANT | CODER | QA | OPS | CUSTOM
  description    TEXT,
  status         TEXT        NOT NULL DEFAULT 'ACTIVE',   -- ACTIVE | INACTIVE | SUSPENDED
  platform       TEXT,                                    -- e.g. OPENCLAW, CUSTOM
  instance_id    TEXT,                                    -- EC2 instance ID, hostname, etc.
  ip_address     TEXT,                                    -- last seen IP
  last_active_at TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (org_id, slug)
);

CREATE INDEX agents_org_idx   ON agents(org_id);
CREATE INDEX agents_owner_idx ON agents(owner_id);

-- Agent ↔ Project join table
-- An agent's allowed projects must be a subset of its owner's project memberships.
CREATE TABLE IF NOT EXISTS agent_projects (
  agent_id   TEXT NOT NULL REFERENCES agents(id)   ON DELETE CASCADE,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  PRIMARY KEY (agent_id, project_id)
);

CREATE INDEX agent_projects_project_idx ON agent_projects(project_id);

-- Alter api_keys to support both USER and AGENT ownership.
-- owner_type discriminator: 'USER' or 'AGENT'
ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS owner_type TEXT NOT NULL DEFAULT 'USER',
  ADD COLUMN IF NOT EXISTS agent_id   TEXT REFERENCES agents(id) ON DELETE CASCADE;

-- Existing rows are all user-owned — user_id stays NOT NULL for them.
-- New agent keys will have agent_id set and user_id NULL.
-- Relax the NOT NULL on user_id so agent keys can omit it.
ALTER TABLE api_keys ALTER COLUMN user_id DROP NOT NULL;

-- Enforce: exactly one of user_id / agent_id must be set.
ALTER TABLE api_keys ADD CONSTRAINT api_keys_owner_check
  CHECK (
    (owner_type = 'USER'  AND user_id  IS NOT NULL AND agent_id IS NULL) OR
    (owner_type = 'AGENT' AND agent_id IS NOT NULL AND user_id  IS NULL)
  );

CREATE INDEX api_keys_agent_idx ON api_keys(agent_id) WHERE agent_id IS NOT NULL;
