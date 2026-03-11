-- WNW-31: Per-project access control for users
--
-- This migration must work for both:
-- 1. Fresh databases where 001 already creates project_memberships.
-- 2. Older databases that do not have the table yet.
CREATE TABLE IF NOT EXISTS project_memberships (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role       VARCHAR(50) NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, project_id)
);

-- Normalize the table shape for databases that were created from an older
-- definition of this migration.
ALTER TABLE project_memberships
    ALTER COLUMN id TYPE UUID USING id::uuid,
    ALTER COLUMN id SET DEFAULT gen_random_uuid(),
    ALTER COLUMN user_id TYPE UUID USING user_id::uuid,
    ALTER COLUMN project_id TYPE UUID USING project_id::uuid,
    ALTER COLUMN role TYPE VARCHAR(50),
    ALTER COLUMN role SET DEFAULT 'member';

CREATE INDEX IF NOT EXISTS project_memberships_user_idx
    ON project_memberships(user_id);

CREATE INDEX IF NOT EXISTS project_memberships_project_idx
    ON project_memberships(project_id);
