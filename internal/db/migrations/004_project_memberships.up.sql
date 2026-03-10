-- WNW-31: Per-project access control for users
CREATE TABLE project_memberships (
  id         TEXT        NOT NULL PRIMARY KEY,
  user_id    TEXT        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
  project_id TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  role       TEXT        NOT NULL DEFAULT 'member',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, project_id)
);

CREATE INDEX project_memberships_user_idx    ON project_memberships(user_id);
CREATE INDEX project_memberships_project_idx ON project_memberships(project_id);
