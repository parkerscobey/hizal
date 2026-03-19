CREATE TABLE agent_types (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id         UUID REFERENCES orgs(id) ON DELETE CASCADE,
  name           VARCHAR(255) NOT NULL,
  slug           VARCHAR(100) NOT NULL,
  base_type      VARCHAR(100),
  description    TEXT,
  inject_filters JSONB NOT NULL DEFAULT '{}',
  search_filters JSONB NOT NULL DEFAULT '{}',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (org_id, slug)
);

INSERT INTO agent_types (org_id, name, slug, base_type, description, inject_filters, search_filters) VALUES
  (NULL, 'Dev', 'dev', NULL, 'Software development agent. Full context injection. For OpenCode and coding agents.', '{"include_scopes":["AGENT","PROJECT","ORG"],"include_chunk_types":["KNOWLEDGE","CONVENTION","IDENTITY","PRINCIPLE","DECISION"]}', '{"include_scopes":["AGENT","PROJECT","ORG"]}'),
  (NULL, 'Admin', 'admin', NULL, 'Business and ops agent. AGENT + ORG scope only. No project code conventions.', '{"include_scopes":["AGENT","ORG"],"include_chunk_types":["KNOWLEDGE","IDENTITY","PRINCIPLE"]}', '{"include_scopes":["AGENT","ORG"]}'),
  (NULL, 'Research', 'research', NULL, 'Investigation agent. Identity injection only. Full search access.', '{"include_scopes":["AGENT"],"include_chunk_types":["IDENTITY"]}', '{"include_scopes":["AGENT","PROJECT","ORG"]}'),
  (NULL, 'Orchestrator', 'orchestrator', NULL, 'Long-running coordination agent (OpenClaw). Full context injection. Spawns and steers subagents. Human interface into dev cycle.', '{"include_scopes":["AGENT","PROJECT","ORG"],"include_chunk_types":["KNOWLEDGE","CONVENTION","IDENTITY","PRINCIPLE","DECISION"]}', '{"include_scopes":["AGENT","PROJECT","ORG"]}');

ALTER TABLE agents ADD COLUMN type_id UUID REFERENCES agent_types(id) ON DELETE SET NULL;
