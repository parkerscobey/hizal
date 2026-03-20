ALTER TABLE context_chunks ADD COLUMN always_inject BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chunk_types    ADD COLUMN default_always_inject BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE context_chunks cc
SET always_inject = TRUE
FROM chunk_types ct
WHERE ct.name = cc.chunk_type
  AND cc.inject_audience IS NOT NULL
  AND (
    cc.inject_audience->'rules' @> '[{"all":true}]'::jsonb
    OR cc.inject_audience->'rules' @> '[{"agent_types":["dev"]}]'::jsonb
  );

UPDATE chunk_types
SET default_always_inject = TRUE
WHERE default_inject_audience IS NOT NULL
  AND (
    default_inject_audience->'rules' @> '[{"all":true}]'::jsonb
  );

DROP INDEX IF EXISTS idx_context_chunks_inject_audience;

ALTER TABLE context_chunks DROP COLUMN inject_audience;
ALTER TABLE chunk_types    DROP COLUMN default_inject_audience;
