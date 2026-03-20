DO $$ BEGIN
  CREATE TYPE chunk_visibility AS ENUM ('private', 'public');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE context_chunks
  ADD COLUMN IF NOT EXISTS visibility chunk_visibility NOT NULL DEFAULT 'private';

CREATE INDEX IF NOT EXISTS idx_context_chunks_visibility
  ON context_chunks (visibility)
  WHERE visibility = 'public';
