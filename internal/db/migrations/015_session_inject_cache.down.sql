DROP INDEX IF EXISTS idx_context_chunks_inject_audience_scope;
ALTER TABLE sessions DROP COLUMN IF EXISTS inject_set;