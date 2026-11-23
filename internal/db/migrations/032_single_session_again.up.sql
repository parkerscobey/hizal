-- 032_single_session_again (up) — re-assert one active session per agent.
--
-- Reverts #142 / migration 030_multi_session. Parker's call, 2026-11-23: the
-- single-active-session rule was the better invariant, and concurrent sessions
-- per agent bought nothing the team wanted. So the partial unique index that
-- 010_sessions created, and 030 dropped, comes back here.
--
-- DELIBERATE CONSEQUENCE: a second start_session fails again. The 030 rollback
-- path warned that recreating this index fails if an agent already holds more
-- than one active session — that is not a footnote here, it is the point. The
-- revert does NOT end anyone's sessions: collapse is a separate, visible
-- operation (end the extras), never a side effect of a schema change.
--
-- (agent_id is NOT NULL, so the partial predicate is just status = 'active'.)

CREATE UNIQUE INDEX IF NOT EXISTS sessions_one_active_per_agent
    ON sessions (agent_id)
    WHERE status = 'active';