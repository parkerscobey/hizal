package api

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type skillDocument struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Purpose     string `json:"purpose"`
	Format      string `json:"format"`
	Markdown    string `json:"markdown"`
}

type skillSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Purpose     string `json:"purpose"`
	Format      string `json:"format"`
	URL         string `json:"url"`
}

type SkillHandlers struct {
	pool *pgxpool.Pool
}

func NewSkillHandlers(pool *pgxpool.Pool) *SkillHandlers {
	return &SkillHandlers{pool: pool}
}

var skillCatalog = map[string]skillDocument{
	"hizal-onboard": {
		ID:          "hizal-onboard",
		Title:       "Hizal Onboard",
		Description: "Onboard to a project with Hizal by selecting project scope and reading high-signal context first.",
		Purpose:     "Fast project orientation before coding.",
		Format:      "markdown",
		Markdown: `---
name: hizal-onboard
description: Onboard to a project with Hizal by listing projects, selecting the right project_id, searching for architecture and status context, and summarizing the current mental model.
---

# Hizal Onboard

Use this skill when the user wants a fast project orientation before coding.

Use it for requests like:
- "Onboard me to this project"
- "Get me up to speed"
- "What is the current state of this system?"

## Setup

Expect a Hizal MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Do not assume the active project. Start by discovering or confirming the correct ` + "`project_id`" + `.

## Workflow

1. Discover the target project.
   - Call ` + "`list_projects`" + ` if the project is not explicit.
   - Use the project name and description to choose the correct ` + "`project_id`" + `.
2. Search for high-level context first.
   - ` + "`search_context(query=\"project overview architecture\", project_id=\"<project_id>\", limit=5)`" + `
   - ` + "`search_context(query=\"design decisions conventions\", project_id=\"<project_id>\", limit=5)`" + `
   - ` + "`search_context(query=\"current status roadmap\", project_id=\"<project_id>\", limit=5)`" + `
3. Read the most relevant chunks in full with ` + "`read_context`" + `.
4. Expand into the major feature or domain areas you discover.
5. Check ` + "`get_context_versions`" + ` for foundational chunks if freshness matters.
6. Return a concise mental model covering:
   - project purpose and current state
   - major architecture and data flow
   - conventions and constraints
   - open questions or gaps
   - where to start for the user's task
7. If you create a useful synthesis that does not already exist, write it back with ` + "`write_context`" + `.

## Notes

- Prefer existing Hizal context before reading large portions of the repo.
- Use ` + "`project_id`" + ` on MCP tool calls instead of relying on connection-level project headers.
- If Hizal context is sparse, fall back to repo docs, README files, and targeted code search.
`,
	},
	"hizal-research": {
		ID:          "hizal-research",
		Title:       "Hizal Research",
		Description: "Research a topic with Hizal by checking existing context first, filling gaps, and writing back a focused summary.",
		Purpose:     "Research, discovery, and background gathering tied to Hizal.",
		Format:      "markdown",
		Markdown: `---
name: hizal-research
description: Research a topic with Hizal by checking existing context first, reading the relevant chunks, filling gaps from the repo or web, and writing back a focused summary.
---

# Hizal Research

Use this skill when the user wants research, discovery, or background gathering tied to Hizal.

Use it for requests like:
- "Research X"
- "What do we know about X?"
- "Look into X and save the findings"

## Setup

Expect a Hizal MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Choose the target ` + "`project_id`" + ` explicitly. If the project is unclear, call ` + "`list_projects`" + ` first.

## Workflow

1. Resolve the project with ` + "`list_projects`" + ` when needed.
2. Search Hizal before doing new work.
   - ` + "`search_context(query=\"<topic>\", project_id=\"<project_id>\", limit=5)`" + `
3. Read the top matches with ` + "`read_context`" + `.
4. If the answer is already present and recent, use it directly.
5. If context is incomplete, gather the missing facts from the codebase, docs, or the web.
6. Write back a focused synthesis with ` + "`write_context`" + `.
7. Return the answer with the relevant chunk IDs when useful.

## Notes

- Avoid writing duplicate chunks.
- Keep chunks narrow and factual.
- Include a source path or URL when you add new information.
- Use ` + "`project_id`" + ` on MCP tool calls instead of connection-level project headers.
`,
	},
	"hizal-plan": {
		ID:          "hizal-plan",
		Title:       "Hizal Plan",
		Description: "Build a task plan with Hizal by reviewing prior decisions and constraints, then saving the resulting plan.",
		Purpose:     "Concrete implementation or investigation planning grounded in Hizal context.",
		Format:      "markdown",
		Markdown: `---
name: hizal-plan
description: Build a task plan with Hizal by reviewing prior decisions and constraints, drafting an approach, validating it against existing context, and saving the resulting plan.
---

# Hizal Plan

Use this skill when the user wants a concrete implementation or investigation plan grounded in Hizal context.

Use it for requests like:
- "Plan how to implement X"
- "Create a plan for X"
- "How should we approach X?"

## Setup

Expect a Hizal MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Resolve the ` + "`project_id`" + ` explicitly for all project-scoped MCP calls.

## Workflow

1. Discover the target project with ` + "`list_projects`" + ` if needed.
2. Search for related decisions, constraints, and prior work with ` + "`search_context`" + `.
3. Read the relevant chunks in full.
4. Draft a plan with:
   - goal
   - approach
   - concrete steps
   - dependencies
   - risks or open questions
   - success criteria
5. Validate the draft against known conventions or constraints from Hizal.
6. Save the finalized plan with ` + "`write_context`" + `.
7. If the plan changes materially, update it with ` + "`update_context`" + `.

## Notes

- Plans should reflect known constraints from Hizal, not just a fresh guess.
- Include ticket IDs or other traceable references in the saved plan when available.
- Use ` + "`project_id`" + ` on MCP tool calls instead of connection-level project headers.
`,
	},
	"hizal-compact": {
		ID:          "hizal-compact",
		Title:       "Hizal Compact",
		Description: "Compact overlapping Hizal context into a higher-signal summary and clean up redundant chunks carefully.",
		Purpose:     "Reduce noisy or overlapping Hizal context on the same topic.",
		Format:      "markdown",
		Markdown: `---
name: hizal-compact
description: Compact overlapping Hizal context by gathering related chunks, producing a higher-signal summary, writing it back, and superseding or deleting redundant chunks carefully.
---

# Hizal Compact

Use this skill when Hizal has too many overlapping or low-signal chunks on the same topic.

Use it for requests like:
- "Compact the context for X"
- "Merge the research on X"
- "Clean up noisy Hizal chunks"

## Setup

Expect a Hizal MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Resolve the ` + "`project_id`" + ` explicitly for all project-scoped MCP calls.

## Workflow

1. Identify related chunks with ` + "`search_context(query=\"<topic>\", project_id=\"<project_id>\", limit=20)`" + `.
2. Read the candidates before changing anything.
3. Fetch the selected material with ` + "`compact_context(ids=[...], project_id=\"<project_id>\")`" + `.
4. Produce one clear summary that preserves important facts, decisions, and references.
5. Write the summary with ` + "`write_context`" + `.
6. Supersede or delete originals only after confirming the new chunk fully covers them.
7. Report what was merged and the new chunk ID.

## Notes

- Never delete unread chunks.
- Preserve traceability by referencing the original chunk IDs.
- Prefer updating stale chunks with a superseded note over deleting them when history matters.
`,
	},
	"hizal-seed": {
		ID:          "hizal-seed",
		Title:       "Hizal Seed",
		Description: "Seed a new or empty Hizal project with foundational context by scanning repos, docs, and configs thoroughly, then writing structured chunks across a planned taxonomy.",
		Purpose:     "Populate a new Hizal project with its initial knowledge base from a codebase.",
		Format:      "markdown",
		Markdown: `---
name: hizal-seed
description: Seed a new or empty Hizal project with foundational context by scanning repos, docs, and configs thoroughly, then writing structured chunks across a planned taxonomy. Use when a project has no context yet, when onboarding a new codebase to Hizal, or when the user says "seed this project" or "backfill context."
---

# Hizal Seed

Use this skill to populate a Hizal project with its initial knowledge base. This is the "day zero" workflow — turning an empty project into something agents can actually onboard from.

## When To Use

- A new Hizal project has been created but has no chunks
- A codebase has been added to Hizal and needs initial context
- The user asks to "seed," "backfill," or "bootstrap" a project
- ` + "`search_context(query=\"*\")`" + ` returns empty or near-empty results

## Setup

Expect a Hizal MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Resolve the ` + "`project_id`" + ` explicitly. If unclear, call ` + "`list_projects`" + ` first.

## Workflow

### 1. Assess Current State

Check what already exists:

` + "```" + `
search_context(query="*", project_id="<project_id>", limit=50)
` + "```" + `

If chunks already exist, this is not a seed — use hizal-research or hizal-compact instead.

### 2. Gather Source Material

Scan the target codebase thoroughly. Do not skim — read deeply:

- **Documentation:** README, docs/, architecture docs, ADRs, CONTRIBUTING
- **Configuration:** package.json/go.mod, Dockerfile, CI/CD configs, env files
- **Source code:** Entry points, routers, models, middleware, auth, schema/migrations
- **Infrastructure:** Deploy workflows, container configs, cloud resources, DNS
- **Tests:** Test structure reveals architecture and expected behavior

### 3. Plan the Taxonomy

Before writing any chunks, draft a set of ` + "`query_key`" + ` categories. Good taxonomies are:

- **Exhaustive** — every important topic has a home
- **Non-overlapping** — categories are distinct
- **Searchable** — an agent looking for a topic would find it

Recommended starting categories (adapt to the project):

- ` + "`architecture`" + ` — System overview, components, tech stack, product vision
- ` + "`domain-model`" + ` — Entity glossary, relationships, business concepts
- ` + "`api-routes`" + ` — Endpoints, request/response shapes, middleware
- ` + "`auth`" + ` — Authentication, authorization, roles, permissions
- ` + "`database-schema`" + ` — Tables, migrations, indexes, constraints
- ` + "`code-patterns`" + ` — Conventions, project structure, dependencies, error handling
- ` + "`frontend-patterns`" + ` — UI framework, components, state management, styling
- ` + "`deployment`" + ` — CI/CD, Docker, cloud infra, env vars, DNS

Drop categories that do not apply. Add domain-specific categories as needed.

### 4. Write Chunks Systematically

Work through the taxonomy one category at a time. For each chunk:

- **Title:** Descriptive — "Authentication and Authorization Model" not "Auth"
- **Content:** Concise but thorough. An agent should be able to start working from this.
- **source_file:** Primary file or directory the knowledge came from
- **gotchas:** Non-obvious constraints, footguns, known issues. Highest-value field.
- **related:** Other query_keys this connects to

Aim for 500-1500 words per chunk. One concept per chunk. Multiple chunks per category is fine.

### 5. Verify Coverage

After writing all chunks:

` + "```" + `
search_context(query="*", project_id="<project_id>", limit=50)
` + "```" + `

Check every taxonomy category has at least one chunk, no major area is unrepresented, and related fields form a connected graph.

### 6. Report

Summarize: total chunks created, categories covered, gaps for future research.

## Notes

- This skill writes many chunks in one session. Be methodical.
- Do not duplicate information across chunks. Reference, do not repeat.
- Include dependency and design system information.
- Always document the deploy pipeline.
- Seed chunks are foundational — they will be read hundreds of times. Make them good.
`,
	},
	"hizal-review": {
		ID:          "hizal-review",
		Title:       "Hizal Review",
		Description: "Review Hizal context quality by rating accuracy and usefulness, then correcting or removing low-value entries.",
		Purpose:     "Quality audit of stored Hizal knowledge.",
		Format:      "markdown",
		Markdown: `---
name: hizal-review
description: Review Hizal context quality by finding relevant chunks, rating their accuracy and usefulness, updating stale content, and removing low-value entries when justified.
---

# Hizal Review

Use this skill when the user wants a quality audit of stored Hizal knowledge.

Use it for requests like:
- "Review the Hizal chunks for X"
- "Audit the context for this area"
- "Clean up stale knowledge"

## Setup

Expect a Hizal MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Resolve the ` + "`project_id`" + ` explicitly for all project-scoped MCP calls.

## Workflow

1. Find the target chunks with ` + "`search_context`" + `.
2. Read each chunk fully with ` + "`read_context`" + `.
3. Check freshness with ` + "`get_context_versions`" + ` when needed.
4. Rate usefulness and correctness with ` + "`review_context`" + `.
5. Update stale chunks with ` + "`update_context`" + ` when a correction is clear.
6. Delete only when a chunk is clearly wrong, redundant, or replaced.
7. Return the review summary, including what changed.

## Notes

- Prefer correcting over deleting when the history is still useful.
- Be explicit about why a chunk is stale or low value.
- Use ` + "`project_id`" + ` on MCP tool calls instead of connection-level project headers.
`,
	},
}

func listSkillSummaries(agentID *string, agentScoped bool) []skillSummary {
	skills := make([]skillSummary, 0, len(skillCatalog))
	for _, skill := range skillCatalog {
		url := fmt.Sprintf("/api/v1/skills/%s", skill.ID)
		if agentScoped && agentID != nil && *agentID != "" {
			url = fmt.Sprintf("/api/v1/agents/%s/skills/%s", *agentID, skill.ID)
		}
		skills = append(skills, skillSummary{
			ID:          skill.ID,
			Title:       skill.Title,
			Description: skill.Description,
			Purpose:     skill.Purpose,
			Format:      skill.Format,
			URL:         url,
		})
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].ID < skills[j].ID
	})
	return skills
}

func lookupSkillDocument(id string) (skillDocument, bool) {
	skill, ok := skillCatalog[id]
	return skill, ok
}

// GET /api/v1/skills
func (h *SkillHandlers) List(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"skills": listSkillSummaries(nil, false),
	})
}

// GET /api/v1/skills/{id}
func (h *SkillHandlers) Get(w http.ResponseWriter, r *http.Request) {
	skillID := chi.URLParam(r, "id")
	skill, ok := lookupSkillDocument(skillID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, skill)
}

// GET /api/v1/agents/{id}/skills/{skillId}
func (h *SkillHandlers) GetForAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	if h.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
		return
	}
	if _, _, err := requireAgentAccess(r, h.pool, agentID, "owner", "admin", "member", "viewer"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	skillID := chi.URLParam(r, "skillId")
	skill, ok := lookupSkillDocument(skillID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, skill)
}
