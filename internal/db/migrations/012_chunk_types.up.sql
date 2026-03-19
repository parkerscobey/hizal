CREATE TABLE chunk_types (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                  UUID REFERENCES orgs(id) ON DELETE CASCADE,
  name                    VARCHAR(255) NOT NULL,
  slug                    VARCHAR(100) NOT NULL,
  description             TEXT,
  default_scope           VARCHAR(20) NOT NULL DEFAULT 'PROJECT',
  default_always_inject   BOOLEAN NOT NULL DEFAULT false,
  consolidation_behavior  VARCHAR(20) NOT NULL DEFAULT 'SURFACE',
  created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (org_id, slug)
);

INSERT INTO chunk_types (org_id, name, slug, description, default_scope, default_always_inject, consolidation_behavior) VALUES
(NULL, 'Identity', 'IDENTITY', 'Agent identity, personality, and core traits', 'AGENT', true, 'KEEP'),
(NULL, 'Memory', 'MEMORY', 'Episodic context, conversation history, or task-specific memory', 'AGENT', false, 'SURFACE'),
(NULL, 'Knowledge', 'KNOWLEDGE', 'Facts, architecture, and established patterns', 'PROJECT', false, 'KEEP'),
(NULL, 'Convention', 'CONVENTION', 'Coding standards, patterns, and project conventions', 'PROJECT', true, 'KEEP'),
(NULL, 'Principle', 'PRINCIPLE', 'Fundamental org-level truths that agents must follow', 'ORG', true, 'KEEP'),
(NULL, 'Decision', 'DECISION', 'Made decisions with reasoning and context', 'PROJECT', false, 'KEEP'),
(NULL, 'Research', 'RESEARCH', 'Investigation findings and exploration notes', 'PROJECT', false, 'SURFACE'),
(NULL, 'Plan', 'PLAN', 'Planned work, approaches, and task breakdowns', 'PROJECT', false, 'SURFACE'),
(NULL, 'Spec', 'SPEC', 'Specification documents and requirements', 'PROJECT', false, 'SURFACE'),
(NULL, 'Implementation', 'IMPLEMENTATION', 'Implementation notes and code-level documentation', 'PROJECT', false, 'SURFACE'),
(NULL, 'Constraint', 'CONSTRAINT', 'Hard limits and non-negotiable requirements', 'PROJECT', true, 'KEEP'),
(NULL, 'Lesson', 'LESSON', 'Learned lessons and post-mortems', 'PROJECT', false, 'SURFACE');
