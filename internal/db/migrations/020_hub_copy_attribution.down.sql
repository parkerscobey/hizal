ALTER TABLE context_chunks
  DROP COLUMN IF EXISTS source_chunk_id,
  DROP COLUMN IF EXISTS source_org_name;
