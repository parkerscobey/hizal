-- Migration 010: Session Primitive
-- Sessions are opt-in but binding once started.
-- One active session per agent enforced by partial unique index.
-- Sessions are the gating unit for write_* tools and register_focus.

CREATE TABLE session_lifecycles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID REFERENCES orgs(id) ON DELETE CASCADE, -- NULL = global built-in preset
    name        VARCHAR(255) NOT NULL,
    slug        VARCHAR(100) NOT NULL,
    is_default  BOOLEAN NOT NULL DEFAULT FALSE,
    description TEXT,
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, slug)
);

-- Global built-in lifecycle presets (org_id = NULL).
-- These are immutable from the API; orgs can create their own via POST.
INSERT INTO session_lifecycles (org_id, name, slug, is_default, description, config) VALUES
    (NULL, 'Default', 'default', TRUE,
     'General-purpose lifecycle for any agent. No required steps, full context injection. Use when no other preset fits.',
     '{
        "ttl_hours": 8,
        "required_steps": [],
        "consolidation_threshold": 5,
        "inject_scopes": ["AGENT", "PROJECT", "ORG"]
    }'),
    (NULL, 'Dev', 'dev', FALSE,
     'Software development sessions. Requires focus registration before writing to enforce explicit task declaration. Full context injection. Designed for OpenCode and similar coding agents.',
     '{
        "ttl_hours": 8,
        "required_steps": ["register_focus"],
        "consolidation_threshold": 3,
        "inject_scopes": ["AGENT", "PROJECT", "ORG"]
    }'),
    (NULL, 'Admin', 'admin', FALSE,
     'Business and operations agents. Shorter TTL, AGENT + ORG scope only — skips PROJECT code conventions as irrelevant noise. For PPP admin, finance, HR, and client-facing agents.',
     '{
        "ttl_hours": 4,
        "required_steps": ["register_focus"],
        "consolidation_threshold": 2,
        "inject_scopes": ["AGENT", "ORG"]
    }'),
    (NULL, 'Orchestrator', 'orchestrator', FALSE,
     'Long-running coordination sessions for OpenClaw orchestrator agents. Full context injection, no required steps, extended TTL. Designed for human-facing agents that spawn and steer subagents across a development cycle. The orchestrator stays alive, talks to the human, and reads context written by its subagents — without being in any individual subagent session.',
     '{
        "ttl_hours": 24,
        "required_steps": [],
        "consolidation_threshold": 10,
        "inject_scopes": ["AGENT", "PROJECT", "ORG"]
    }');

CREATE TABLE sessions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id              UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    project_id            UUID REFERENCES projects(id) ON DELETE SET NULL,
    org_id                UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    lifecycle_id          UUID REFERENCES session_lifecycles(id) ON DELETE SET NULL,
    status                VARCHAR(20) NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active', 'ended', 'expired')),
    focus_task            TEXT,
    chunks_written        INT NOT NULL DEFAULT 0,
    chunks_read           INT NOT NULL DEFAULT 0,
    consolidation_done    BOOLEAN NOT NULL DEFAULT FALSE,
    resume_count          INT NOT NULL DEFAULT 0,
    expires_at            TIMESTAMPTZ NOT NULL,
    started_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at              TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Enforce one active session per agent at the DB level.
CREATE UNIQUE INDEX sessions_one_active_per_agent
    ON sessions (agent_id)
    WHERE status = 'active';

CREATE INDEX sessions_agent_id_idx ON sessions (agent_id);
CREATE INDEX sessions_status_idx ON sessions (status);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at) WHERE status = 'active';
