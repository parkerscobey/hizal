ALTER TABLE sessions ADD COLUMN IF NOT EXISTS inject_set JSONB;

CREATE INDEX IF NOT EXISTS idx_context_chunks_inject_audience_scope
  ON context_chunks (scope, org_id) WHERE inject_audience IS NOT NULL;