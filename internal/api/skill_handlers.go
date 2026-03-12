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
	"winnow-onboard": {
		ID:          "winnow-onboard",
		Title:       "Winnow Onboard",
		Description: "Onboard to a project with Winnow by selecting project scope and reading high-signal context first.",
		Purpose:     "Fast project orientation before coding.",
		Format:      "markdown",
		Markdown: `---
name: winnow-onboard
description: Onboard to a project with Winnow by listing projects, selecting the right project_id, searching for architecture and status context, and summarizing the current mental model.
---

# Winnow Onboard

Use this skill when the user wants a fast project orientation before coding.

Use it for requests like:
- "Onboard me to this project"
- "Get me up to speed"
- "What is the current state of this system?"

## Setup

Expect a Winnow MCP server to be configured with:
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

- Prefer existing Winnow context before reading large portions of the repo.
- Use ` + "`project_id`" + ` on MCP tool calls instead of relying on connection-level project headers.
- If Winnow context is sparse, fall back to repo docs, README files, and targeted code search.
`,
	},
	"winnow-research": {
		ID:          "winnow-research",
		Title:       "Winnow Research",
		Description: "Research a topic with Winnow by checking existing context first, filling gaps, and writing back a focused summary.",
		Purpose:     "Research, discovery, and background gathering tied to Winnow.",
		Format:      "markdown",
		Markdown: `---
name: winnow-research
description: Research a topic with Winnow by checking existing context first, reading the relevant chunks, filling gaps from the repo or web, and writing back a focused summary.
---

# Winnow Research

Use this skill when the user wants research, discovery, or background gathering tied to Winnow.

Use it for requests like:
- "Research X"
- "What do we know about X?"
- "Look into X and save the findings"

## Setup

Expect a Winnow MCP server to be configured with:
- ` + "`Authorization: Bearer <api-key>`" + `

Choose the target ` + "`project_id`" + ` explicitly. If the project is unclear, call ` + "`list_projects`" + ` first.

## Workflow

1. Resolve the project with ` + "`list_projects`" + ` when needed.
2. Search Winnow before doing new work.
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
	"winnow-plan": {
		ID:          "winnow-plan",
		Title:       "Winnow Plan",
		Description: "Build a task plan with Winnow by reviewing prior decisions and constraints, then saving the resulting plan.",
		Purpose:     "Concrete implementation or investigation planning grounded in Winnow context.",
		Format:      "markdown",
		Markdown: `---
name: winnow-plan
description: Build a task plan with Winnow by reviewing prior decisions and constraints, drafting an approach, validating it against existing context, and saving the resulting plan.
---

# Winnow Plan

Use this skill when the user wants a concrete implementation or investigation plan grounded in Winnow context.

Use it for requests like:
- "Plan how to implement X"
- "Create a plan for X"
- "How should we approach X?"

## Setup

Expect a Winnow MCP server to be configured with:
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
5. Validate the draft against known conventions or constraints from Winnow.
6. Save the finalized plan with ` + "`write_context`" + `.
7. If the plan changes materially, update it with ` + "`update_context`" + `.

## Notes

- Plans should reflect known constraints from Winnow, not just a fresh guess.
- Include ticket IDs or other traceable references in the saved plan when available.
- Use ` + "`project_id`" + ` on MCP tool calls instead of connection-level project headers.
`,
	},
	"winnow-compact": {
		ID:          "winnow-compact",
		Title:       "Winnow Compact",
		Description: "Compact overlapping Winnow context into a higher-signal summary and clean up redundant chunks carefully.",
		Purpose:     "Reduce noisy or overlapping Winnow context on the same topic.",
		Format:      "markdown",
		Markdown: `---
name: winnow-compact
description: Compact overlapping Winnow context by gathering related chunks, producing a higher-signal summary, writing it back, and superseding or deleting redundant chunks carefully.
---

# Winnow Compact

Use this skill when Winnow has too many overlapping or low-signal chunks on the same topic.

Use it for requests like:
- "Compact the context for X"
- "Merge the research on X"
- "Clean up noisy Winnow chunks"

## Setup

Expect a Winnow MCP server to be configured with:
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
	"winnow-review": {
		ID:          "winnow-review",
		Title:       "Winnow Review",
		Description: "Review Winnow context quality by rating accuracy and usefulness, then correcting or removing low-value entries.",
		Purpose:     "Quality audit of stored Winnow knowledge.",
		Format:      "markdown",
		Markdown: `---
name: winnow-review
description: Review Winnow context quality by finding relevant chunks, rating their accuracy and usefulness, updating stale content, and removing low-value entries when justified.
---

# Winnow Review

Use this skill when the user wants a quality audit of stored Winnow knowledge.

Use it for requests like:
- "Review the Winnow chunks for X"
- "Audit the context for this area"
- "Clean up stale knowledge"

## Setup

Expect a Winnow MCP server to be configured with:
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
