-- Migration 030: allow multiple active sessions per agent (#142).
--
-- 010_sessions created `sessions_one_active_per_agent`, a partial UNIQUE index
-- (agent_id WHERE status='active'), so an agent could have at most one active
-- session. Parker wants concurrent sessions per agent — the same conversation
-- that produced cross-org sharing (#141). This drops that index.
--
-- The read side does NOT guess: with N>1 active sessions, `get_active_session`
-- returns an error listing them and the caller disambiguates by session_id
-- (mirroring the account resolvers: refuse on ambiguity rather than pick). With
-- exactly one, behaviour is unchanged.

DROP INDEX IF EXISTS sessions_one_active_per_agent;
