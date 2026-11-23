-- 032_single_session_again (down) — drop the single-active-session index.
--
-- Forward-fixes to the #142 / migration 030 state: an agent may hold several
-- active sessions, and get_active_session reports ambiguity rather than picking.

DROP INDEX IF EXISTS sessions_one_active_per_agent;