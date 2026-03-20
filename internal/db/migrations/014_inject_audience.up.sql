ALTER TABLE context_chunks ADD COLUMN inject_audience JSONB;
ALTER TABLE chunk_types    ADD COLUMN default_inject_audience JSONB;

UPDATE context_chunks
SET inject_audience = '{"rules":[{"all":true}]}'::jsonb
WHERE always_inject = TRUE;

UPDATE chunk_types
SET default_inject_audience = '{"rules":[{"all":true}]}'::jsonb
WHERE default_always_inject = TRUE;

ALTER TABLE context_chunks DROP COLUMN always_inject;
ALTER TABLE chunk_types    DROP COLUMN default_always_inject;

CREATE INDEX idx_context_chunks_inject_audience ON context_chunks ((inject_audience IS NOT NULL))
  WHERE inject_audience IS NOT NULL;

UPDATE chunk_types SET default_inject_audience = '{"rules":[{"all":true}]}'::jsonb
WHERE name IN ('IDENTITY', 'CONVENTION', 'PRINCIPLE');
