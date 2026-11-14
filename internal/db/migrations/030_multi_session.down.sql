-- Migration 030 rollback: re-assert one active session per agent (#142).
--
-- Recreating the partial unique index will FAIL if any agent currently has more
-- than one active session — that is intentional: rolling back a multi-session
-- database to single-session is a deliberate operation, not a silent collapse of
-- someone's live sessions. End the extra sessions first, then roll back.
--
-- Back to the 010_sessions definition exactly (agent_id is NOT NULL, so the
-- partial predicate is just status = 'active').

CREATE UNIQUE INDEX IF NOT EXISTS sessions_one_active_per_agent
    ON sessions (agent_id)
    WHERE status = 'active';
