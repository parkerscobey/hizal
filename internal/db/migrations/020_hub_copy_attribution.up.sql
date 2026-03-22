ALTER TABLE context_chunks
  ADD COLUMN IF NOT EXISTS source_chunk_id TEXT,
  ADD COLUMN IF NOT EXISTS source_org_name TEXT;
