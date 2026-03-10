-- Dev seed data for local testing
-- Safe to run multiple times (idempotent via ON CONFLICT / NOT EXISTS).

-- Stable UUIDs for repeatable local data
-- org:      11111111-1111-1111-1111-111111111111
-- user:     22222222-2222-2222-2222-222222222222
-- project:  33333333-3333-3333-3333-333333333333
-- api key:  66666666-6666-6666-6666-666666666666
-- chunk #1: 44444444-4444-4444-4444-444444444444
-- chunk #2: 55555555-5555-5555-5555-555555555555
--
-- Demo local JWT login:
--   email:    agent@acme.dev
--   password: localdev123
-- Stored password hash (bcrypt):
--   $2y$10$w54oge2PmHYmaEF9S8PGAe/Rydp4/gbb./wuIRfXxs4Hfx5effFiu
--
-- Demo plaintext API key for local auth:
--   ctx_acme-dev_demo-local-key
-- Stored hash (SHA-256 hex):
--   3c2a2a1d9690fbf9b353e482ffbb329afef8b9cf556fef3ea2f17a301d1fd54e

INSERT INTO orgs (id, name, slug, tier)
VALUES ('11111111-1111-1111-1111-111111111111', 'Acme Dev Org', 'acme-dev', 'free')
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    slug = EXCLUDED.slug,
    tier = EXCLUDED.tier,
    updated_at = NOW();

INSERT INTO users (id, email, name, password_hash)
VALUES (
  '22222222-2222-2222-2222-222222222222',
  'agent@acme.dev',
  'Acme Agent',
  '$2y$10$w54oge2PmHYmaEF9S8PGAe/Rydp4/gbb./wuIRfXxs4Hfx5effFiu'
)
ON CONFLICT (id) DO UPDATE
SET email = EXCLUDED.email,
    name = EXCLUDED.name,
    password_hash = EXCLUDED.password_hash,
    updated_at = NOW();

INSERT INTO org_memberships (user_id, org_id, role)
VALUES ('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'admin')
ON CONFLICT (user_id, org_id) DO NOTHING;

INSERT INTO projects (id, org_id, name, slug)
VALUES ('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'Default', 'default')
ON CONFLICT (id) DO UPDATE
SET org_id = EXCLUDED.org_id,
    name = EXCLUDED.name,
    slug = EXCLUDED.slug,
    updated_at = NOW();

INSERT INTO project_memberships (user_id, project_id, role)
VALUES ('22222222-2222-2222-2222-222222222222', '33333333-3333-3333-3333-333333333333', 'owner')
ON CONFLICT (user_id, project_id) DO NOTHING;

INSERT INTO api_keys (
  id,
  user_id,
  key_hash,
  name,
  scope_all_projects,
  allowed_project_ids,
  permissions
)
VALUES (
  '66666666-6666-6666-6666-666666666666',
  '22222222-2222-2222-2222-222222222222',
  '3c2a2a1d9690fbf9b353e482ffbb329afef8b9cf556fef3ea2f17a301d1fd54e',
  'Local Demo Key',
  FALSE,
  ARRAY['33333333-3333-3333-3333-333333333333']::uuid[],
  '{"context":{"write":true,"read":true,"update":true,"delete":true,"review":true}}'::jsonb
)
ON CONFLICT (key_hash) DO UPDATE
SET
  user_id = EXCLUDED.user_id,
  name = EXCLUDED.name,
  scope_all_projects = EXCLUDED.scope_all_projects,
  allowed_project_ids = EXCLUDED.allowed_project_ids,
  permissions = EXCLUDED.permissions,
  updated_at = NOW();

INSERT INTO context_chunks (
  id,
  project_id,
  query_key,
  title,
  content,
  source_file,
  source_lines,
  gotchas,
  related,
  created_by_agent
)
VALUES
(
  '44444444-4444-4444-4444-444444444444',
  '33333333-3333-3333-3333-333333333333',
  'auth.middleware',
  'Local auth flow behavior',
  '{"text":"Local testing supports password login at /v1/auth/login for seeded users and Bearer API key auth for context/MCP routes. API keys belong to users and can be scoped to selected projects."}'::jsonb,
  'internal/api/auth_handlers.go',
  '{"start": 19, "end": 160}'::jsonb,
  '["JWT routes and API key routes are separate; use the JWT to manage orgs, projects, and keys, then use the API key for MCP and context APIs."]'::jsonb,
  '[]'::jsonb,
  'codex-seed'
),
(
  '55555555-5555-5555-5555-555555555555',
  '33333333-3333-3333-3333-333333333333',
  'db.migrations',
  'Database migration baseline',
  '{"text":"Initial schema enables vector + pgcrypto extensions and creates org/user/project/context tables."}'::jsonb,
  'internal/db/migrations/001_initial_schema.up.sql',
  '{"start": 1, "end": 140}'::jsonb,
  '["pgvector extension must be available in the Postgres image."]'::jsonb,
  '["44444444-4444-4444-4444-444444444444"]'::jsonb,
  'codex-seed'
)
ON CONFLICT (id) DO UPDATE
SET
  project_id = EXCLUDED.project_id,
  query_key = EXCLUDED.query_key,
  title = EXCLUDED.title,
  content = EXCLUDED.content,
  source_file = EXCLUDED.source_file,
  source_lines = EXCLUDED.source_lines,
  gotchas = EXCLUDED.gotchas,
  related = EXCLUDED.related,
  created_by_agent = EXCLUDED.created_by_agent,
  updated_at = NOW();

-- Remove accidental duplicate seed versions from previous runs
WITH ranked AS (
  SELECT
    ctid,
    ROW_NUMBER() OVER (PARTITION BY chunk_id, version ORDER BY created_at, id) AS rn
  FROM context_versions
  WHERE chunk_id IN (
    '44444444-4444-4444-4444-444444444444',
    '55555555-5555-5555-5555-555555555555'
  )
)
DELETE FROM context_versions cv
USING ranked r
WHERE cv.ctid = r.ctid
  AND r.rn > 1;

INSERT INTO context_versions (chunk_id, version, content, change_note)
SELECT
  '44444444-4444-4444-4444-444444444444',
  1,
  '{"text":"Local testing supports password login at /v1/auth/login for seeded users and Bearer API key auth for context/MCP routes. API keys belong to users and can be scoped to selected projects."}'::jsonb,
  'seed: initial snapshot'
WHERE NOT EXISTS (
  SELECT 1
  FROM context_versions
  WHERE chunk_id = '44444444-4444-4444-4444-444444444444'
    AND version = 1
);

INSERT INTO context_versions (chunk_id, version, content, change_note)
SELECT
  '55555555-5555-5555-5555-555555555555',
  1,
  '{"text":"Initial schema enables vector + pgcrypto extensions and creates org/user/project/context tables."}'::jsonb,
  'seed: initial snapshot'
WHERE NOT EXISTS (
  SELECT 1
  FROM context_versions
  WHERE chunk_id = '55555555-5555-5555-5555-555555555555'
    AND version = 1
);

INSERT INTO context_reviews (chunk_id, task, usefulness, usefulness_note, correctness, correctness_note, action)
SELECT
  '44444444-4444-4444-4444-444444444444',
  'Validate auth flow in local API tests',
  5,
  'Clearly explains how seeded JWT and API key auth fit together.',
  5,
  'Matches the current auth handlers and API key middleware split.',
  'keep'
WHERE NOT EXISTS (
  SELECT 1
  FROM context_reviews
  WHERE chunk_id = '44444444-4444-4444-4444-444444444444'
    AND task = 'Validate auth flow in local API tests'
);
