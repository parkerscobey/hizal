ALTER TABLE agents ADD COLUMN search_filter_overrides JSONB NOT NULL DEFAULT '{}';
