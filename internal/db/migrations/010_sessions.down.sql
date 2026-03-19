DROP INDEX IF EXISTS sessions_expires_at_idx;
DROP INDEX IF EXISTS sessions_status_idx;
DROP INDEX IF EXISTS sessions_agent_id_idx;
DROP INDEX IF EXISTS sessions_one_active_per_agent;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS session_lifecycles;
