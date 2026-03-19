---
name: hizal-seed
description: Seed a new or empty Hizal project with foundational context by scanning repos, docs, and configs thoroughly, then writing structured chunks across a planned taxonomy. Use when a project has no context yet, when onboarding a new codebase to Hizal, or when the user says "seed this project" or "backfill context."
---

# Hizal Seed

Use this skill to populate a Hizal project with its initial knowledge base. This is the "day zero" workflow — turning an empty project into something agents can actually onboard from.

## Session Lifecycle

Start a session at the top of any seeding task — see `hizal-onboard`. End it with `end_session` when done.

## When To Use

- A new Hizal project has been created but has no chunks
- A codebase has been added to Hizal and needs initial context
- The user asks to "seed," "backfill," or "bootstrap" a project
- `search_context(query="*")` returns empty or near-empty results

## Setup

Expect a Hizal MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly. If unclear, call `list_projects` first.

## Workflow

### 1. Assess Current State

Before writing anything, check what already exists:

```
search_context(query="*", project_id="<project_id>", limit=50)
```

If chunks already exist, this is not a seed — use `hizal-research` or `hizal-compact` instead.

### 2. Gather Source Material

Scan the target codebase thoroughly. Do not skim — read deeply:

- **Documentation:** README, docs/, architecture docs, ADRs, CONTRIBUTING
- **Configuration:** package.json/go.mod (dependencies), Dockerfile, CI/CD configs, env files, wrangler.toml, terraform/tofu
- **Source code:** Entry points, router/route definitions, models/types, middleware, auth logic, database schema/migrations
- **Infrastructure:** Deploy workflows, container configs, cloud resources, DNS setup
- **Tests:** Test structure reveals architecture and expected behavior

### 3. Plan the Taxonomy

Before writing any chunks, draft a set of `query_key` categories. Good taxonomies are:

- **Exhaustive** — every important topic has a home
- **Non-overlapping** — categories are distinct, not redundant
- **Searchable** — an agent looking for "how auth works" would find it

Recommended starting taxonomy (adapt to the project):

| query_key | Covers |
|-----------|--------|
| `architecture` | System overview, component diagram, tech stack, product vision |
| `domain-model` | Entity glossary, relationships, business concepts |
| `api-routes` | REST/GraphQL endpoints, request/response shapes, middleware |
| `auth` | Authentication, authorization, roles, permissions, session management |
| `database-schema` | Tables, columns, migrations, indexes, constraints |
| `code-patterns` | Conventions, project structure, dependency choices, error handling |
| `frontend-patterns` | UI framework, components, state management, styling approach |
| `deployment` | CI/CD, Docker, cloud infra, env vars, DNS |
| `mcp-tools` | MCP tool definitions and usage (if applicable) |

Drop categories that do not apply. Add categories for major domain areas specific to the project (e.g., `billing`, `notifications`, `search`).

### 4. Write Chunks Systematically

Work through the taxonomy one category at a time. For each chunk:

- **Title:** Descriptive, specific — not "Auth" but "Authentication and Authorization Model"
- **Content:** Concise but thorough. Include what an agent needs to start working in this area immediately. Prioritize facts over opinions.
- **query_key:** From your planned taxonomy
- **source_file:** The primary file or directory this knowledge came from
- **gotchas:** Non-obvious constraints, footguns, known issues. These are the highest-value part of a chunk — they prevent future agents from making mistakes.
- **related:** Other query_keys this chunk connects to. Helps agents discover adjacent context.

#### Chunk Quality Checklist

Before writing each chunk, confirm:
- [ ] An agent reading only this chunk could start working in this area
- [ ] File paths and concrete references are included
- [ ] At least one gotcha is documented (if any exist)
- [ ] Related query_keys point to connected topics
- [ ] No significant overlap with another planned chunk

#### What Makes a Good Seed Chunk

**Good:** "The API uses chi/v5 for routing. Handlers follow a struct pattern: each domain area has a FooHandlers struct with a *pgxpool.Pool field. Responses use writeJSON(w, status, data) and writeError(w, status, code, msg). No ORM — all SQL is hand-written with pgx."

**Bad:** "The API is written in Go and uses a router." (Too vague — an agent learns nothing actionable.)

**Bad:** A 2000-word dump of every function signature. (Too detailed — this is a seed, not a mirror of the codebase.)

Aim for the level of detail a competent new developer would need on day one.

### 5. Verify Coverage

After writing all chunks, verify the result:

```
search_context(query="*", project_id="<project_id>", limit=50)
```

Check:
- Every planned taxonomy category has at least one chunk
- No major area of the codebase is unrepresented
- Cross-references (related fields) form a connected graph

### 6. Report

Summarize what was seeded:
- Total chunks created
- Categories covered
- Any gaps or areas that need deeper research later

## Chunk Sizing Guidelines

- **One concept per chunk.** A chunk about "auth" should not also cover "deployment."
- **500-1500 words per chunk** is the sweet spot. Shorter chunks lack context; longer ones dilute search relevance.
- **Multiple chunks per category is fine.** "Frontend Architecture" and "Frontend Pages Reference" can coexist under `frontend-patterns`.
- **Prefer more focused chunks** over fewer monolithic ones. Agents search semantically — narrower chunks rank better for specific queries.

## Writing Tool Guidance

Use the right tool for the content you are writing:

| Content type | Tool | Scope | always_inject |
|---|---|---|---|
| Factual project documentation | `write_knowledge` | PROJECT | false |
| Foundational rules every agent must know | `write_convention` | PROJECT | true |
| Raw exploratory notes (reviewed at end_session) | `write_chunk(type="RESEARCH")` | PROJECT | false |
| Key architectural decisions | `write_chunk(type="DECISION")` | PROJECT | false |

Do NOT use `write_memory` or `write_identity` during seeding — seeding is project-level, not agent-level.

## Always-Inject Guardrails

`write_convention` is the only always_inject tool used during seeding. Apply the three-question test:

1. Will this still be true and relevant in 6 months?
2. Does every agent need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no, use `write_knowledge` or `write_chunk` instead. Overuse of always_inject degrades every agent's context window.

## Notes

- This skill writes many chunks in one session. Be methodical — rushing leads to low-quality seeds that agents cannot rely on.
- Do not duplicate information across chunks. If auth is covered in the `auth` chunk, the `api-routes` chunk should reference it, not repeat it.
- Include design system, framework, and dependency information. Agents need to know what tools are available, not just what code exists.
- If the codebase has CI/CD, always document the deploy pipeline. "How do I ship this?" is one of the first questions any agent asks.
- Seed chunks are foundational — they will be read hundreds of times. Invest the time to make them good.
