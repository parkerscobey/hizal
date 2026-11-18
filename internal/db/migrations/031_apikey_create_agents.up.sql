-- 031_apikey_create_agents
--
-- Adds an explicit, opt-in scope that lets an API key create agents (and mint
-- their keys) within its own org. Without this, agent creation is reachable
-- only by a JWT (human session) — so an autonomous agent cannot provision a
-- sibling agent, which is what campfire-based orchestration needs.
--
-- Design constraints this column exists to honour:
--   * OFF by default. Every existing key keeps exactly its current powers.
--   * Org-scoped only. A key can create agents in the ONE org it is bound to
--     (api_keys.org_id); it can never reach another org.
--   * USER-owned keys only. The created agent's owner_id is the key's owning
--     user (api_keys.user_id), which is NOT NULL for user-owned keys. This
--     keeps agents.owner_id NOT NULL intact AND means an AGENT-owned key can
--     never mint sibling agents — no self-replication.
ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS can_create_agents BOOLEAN NOT NULL DEFAULT FALSE;
