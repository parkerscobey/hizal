# Hizal: Skills Specification

## Overview

Skills guide agents through structured workflows. Each skill maps to a phase of the agent work lifecycle and uses the appropriate purpose-built write tools.

The local `skills/` directory contains the canonical SKILL.md files that MCP clients read. This document provides the design intent and coordination notes.

---

## Skill: hizal-seed

**Purpose:** Populate a new Hizal project with foundational context from a codebase.

**Tools used:** `write_knowledge`, `write_convention`

**When to use:**
- A new project has been created but has no chunks
- A codebase is being onboarded to Hizal for the first time
- `search_context(query="*")` returns empty or near-empty results

**Workflow:**
1. **Assess** — `search_context(query="*")` to confirm the project is empty
2. **Gather** — Scan deeply: docs, configs, source code, CI/CD, infra, tests
3. **Plan taxonomy** — Draft query_key categories before writing
4. **Write chunks** — One category at a time via `write_knowledge`
5. **Write conventions** — Identify foundational rules and use `write_convention` (sparingly)
6. **Verify** — Confirm every category has ≥1 chunk
7. **Report** — Summarize total chunks, categories, gaps

**Key guidance:**
- `write_knowledge` for facts (architecture, patterns, decisions)
- `write_convention` only for durable rules that every agent must always know
- Never use `write_convention` for things that change — use `write_knowledge` instead

---

## Skill: hizal-research

**Purpose:** Investigate a topic, filling gaps in existing context.

**Tools used:** `write_knowledge` (for project findings), `write_memory` (for personal observations)

**When to use:**
- Starting any non-trivial task in a brownfield codebase
- Onboarding to a new codebase area
- Before writing code that interacts with unfamiliar systems

**Workflow:**
1. **Search** — Check what already exists: `search_context(query="[topic]", project_id="...")`
2. **Read** — Read the most relevant chunks; check `updated_at` for staleness
3. **Fill gaps** — If context is missing or stale, explore the codebase
4. **Write findings** — `write_knowledge` for facts worth sharing with the team
5. **Write observations** — `write_memory` for personal discoveries and interpretive notes
6. **Link** — Update `related` fields to connect knowledge graph

**Decision guidance:**
- Is this fact about the project that any agent should know? → `write_knowledge`
- Is this a personal observation or working pattern? → `write_memory`
- Is this a durable rule that should always be in context? → Propose via `write_knowledge` with a note; let a human promote to `write_convention`

---

## Skill: hizal-plan

**Purpose:** Create an implementation plan validated against existing context.

**Tools used:** `write_knowledge` (plans are shared)

**When to use:**
- After research is complete, before writing code
- When handing off to another agent
- For any non-trivial feature or change

**Workflow:**
1. **Review** — Read all relevant context chunks for the task
2. **Search decisions** — `search_context(query="[topic]", chunk_type="DECISION")` for key architectural decisions
3. **Draft plan** — Structure with file paths, line numbers, patterns to follow
4. **Validate** — Check plan against stored conventions and constraints
5. **Write** — `write_knowledge(chunk_type="PLAN", ...)` to store the plan

---

## Skill: hizal-compact

**Purpose:** Compress overlapping or noisy context into higher-signal chunks.

**Tools used:** `compact_context`, `write_knowledge`, `delete_context`

**When to use:**
- After 15-20 minutes of continuous work (approaching dumb zone)
- Before ending a session
- When retrieval feels noisy or redundant

**Workflow:**
1. **Fetch** — `compact_context(query="[topic]")` to get all matching chunks
2. **Summarize** — Agent synthesizes client-side into a structured summary
3. **Write back** — `write_knowledge` with the compacted summary
4. **Clean up** — Delete or supersede the original chunks

**Key guidance:**
- Compact within the same chunk_type — don't merge DECISION chunks into KNOWLEDGE summaries
- RESEARCH chunks are more disposable — can discard without promoting
- DECISION chunks should be preserved or promoted, never silently discarded
- Compact one scope at a time (PROJECT, AGENT, or ORG — don't mix)

---

## Skill: hizal-onboard

**Purpose:** Quickly get an agent up to speed on a project.

**Tools used:** `list_projects`, `search_context`, `read_context`

**When to use:**
- Agent starts fresh and needs project context
- Hand-off between agents
- Returning to a project after time away

**Workflow:**
1. **Discover** — `list_projects` to find the target project
2. **Search high-level** — Architecture, conventions, current status
3. **Search decisions** — `search_context(chunk_type="DECISION")` for key architectural choices
4. **Search plans** — `search_context(chunk_type="PLAN")` for in-flight work
5. **Read** — Full read of the most relevant chunks
6. **Build mental model** — Map relationships, identify where the current task fits
7. **Identify gaps** — If foundational context is missing, create it

---

## Skill: hizal-review

**Purpose:** Validate and improve context quality through structured reviews.

**Tools used:** `review_context`, `update_context`, `delete_context`

**When to use:**
- After completing a task that relied on context
- When user provides feedback on agent generation
- During periodic quality audits

**Workflow:**
1. **Identify** — Which chunks helped (or hurt) the task?
2. **Assess** — Rate usefulness (1-5) and correctness (1-5)
3. **Determine action** — `useful`, `needs_update`, `outdated`, `incorrect`
4. **Submit review** — `review_context` with ratings and notes
5. **Fix if needed** — `update_context` for stale content, `delete_context` for incorrect

---

## Skill Coordination Notes

### Write tool routing

| Skill | Primary write tool | Chunk type |
|-------|-------------------|------------|
| hizal-seed | `write_knowledge`, `write_convention` | KNOWLEDGE, CONVENTION |
| hizal-research | `write_knowledge`, `write_memory` | KNOWLEDGE, MEMORY, RESEARCH |
| hizal-plan | `write_knowledge` | PLAN |
| hizal-compact | `write_knowledge` | KNOWLEDGE (compacted) |
| hizal-review | `update_context` (fixes) | (preserves original type) |

### Session integration

- **hizal-onboard** should be the first skill used in any session after `start_session`
- **hizal-compact** should be called before `end_session` if chunks were written
- **hizal-review** should be called after relying on context to complete work

---

*Last updated: 2026-03-19*
