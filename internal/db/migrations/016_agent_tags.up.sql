-- Add tags array to agents for inject_audience targeting.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_agents_tags ON agents USING GIN(tags);
