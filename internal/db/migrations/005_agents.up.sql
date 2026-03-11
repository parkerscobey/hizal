-- WNW-30: Agent model + agent-scoped API keys

-- Agents table
CREATE TABLE IF NOT EXISTS agents (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id         UUID        NOT NULL REFERENCES orgs(id)  ON DELETE CASCADE,
  owner_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           VARCHAR(255) NOT NULL,
  slug           VARCHAR(255) NOT NULL,
  type           VARCHAR(50)  NOT NULL DEFAULT 'CUSTOM',   -- ASSISTANT | CODER | QA | OPS | CUSTOM
  description    TEXT,
  status         VARCHAR(50)  NOT NULL DEFAULT 'ACTIVE',   -- ACTIVE | INACTIVE | SUSPENDED
  platform       VARCHAR(255),                             -- e.g. OPENCLAW, CUSTOM
  instance_id    VARCHAR(255),                             -- EC2 instance ID, hostname, etc.
  ip_address     VARCHAR(255),                             -- last seen IP
  last_active_at TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (org_id, slug)
);

CREATE INDEX IF NOT EXISTS agents_org_idx ON agents(org_id);
CREATE INDEX IF NOT EXISTS agents_owner_idx ON agents(owner_id);

-- Agent <-> Project join table
-- An agent's allowed projects must be a subset of its owner's project memberships.
CREATE TABLE IF NOT EXISTS agent_projects (
  agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  PRIMARY KEY (agent_id, project_id)
);

CREATE INDEX IF NOT EXISTS agent_projects_project_idx ON agent_projects(project_id);

-- Alter api_keys to support both USER and AGENT ownership.
-- owner_type discriminator: 'USER' or 'AGENT'
ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS owner_type VARCHAR(20) NOT NULL DEFAULT 'USER',
  ADD COLUMN IF NOT EXISTS agent_id UUID REFERENCES agents(id) ON DELETE CASCADE;

-- Existing rows are all user-owned and remain valid after relaxing this column.
ALTER TABLE api_keys ALTER COLUMN user_id DROP NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'api_keys_owner_check'
      AND conrelid = 'api_keys'::regclass
  ) THEN
    ALTER TABLE api_keys
      ADD CONSTRAINT api_keys_owner_check
      CHECK (
        (owner_type = 'USER' AND user_id IS NOT NULL AND agent_id IS NULL) OR
        (owner_type = 'AGENT' AND agent_id IS NOT NULL AND user_id IS NULL)
      );
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS api_keys_agent_idx
  ON api_keys(agent_id) WHERE agent_id IS NOT NULL;
