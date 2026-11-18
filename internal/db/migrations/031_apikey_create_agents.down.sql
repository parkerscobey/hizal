-- 031_apikey_create_agents (down)
ALTER TABLE api_keys
  DROP COLUMN IF EXISTS can_create_agents;
