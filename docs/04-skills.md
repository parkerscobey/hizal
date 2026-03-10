# Winnow: Skills Specification

## Overview

Skills guide agents through structured workflows, preventing the "dumb zone" and ensuring context compounds between runs.

---

## Skill: winnow-research

**Purpose:** Guide agents to efficiently gather and create context before planning

**When to use:**
- Starting any non-trivial task in a brownfield codebase
- Onboarding to a new codebase area
- Before writing code that interacts with unfamiliar systems

**Philosophy:**
Research is compression of truth. The goal is to understand the system well enough to write a clear implementation plan.

### Workflow

```
1. SEARCH existing context
   └─> "What do we already know about [topic]?"
   
2. IF found
   └─> Read and summarize existing context
   └─> CHECK metadata: version, updated_at
   └─> IF updated_at is old (>7 days), consider updating
   └─> IF content has gaps, consider updating
   └─> IF outdated info found, use get_context_versions to see history
   
3. IF not found OR gaps exist
   └─> Explore codebase (read files, find patterns)
   └─> Write new context chunk capturing findings
   
4. STRUCTURE context
   └─> What: one-liner description
   └─> Files: key file paths + line numbers
   └─> Gotchas: warnings for future agents
   └─> Related: connected context areas
   
5. COMPOUND
   └─> Link to related contexts (update related field)
   └─> IF updating existing chunk, use update_context with change_note
```

### Context Quality Checklist

Before finishing research, confirm:
- [ ] Can explain what this system/feature does in one sentence
- [ ] Know which files are involved (paths + lines)
- [ ] Understand dependencies and relationships
- [ ] Identified at least one "gotcha" or warning
- [ ] Know what other areas relate to this
- [ ] Checked updated_at — content is recent
- [ ] If content is stale (>7 days old), plan to update it

### Example

**Case A: No existing context**
```
Task: "Add password reset functionality"

1. search_context(query: "password reset")
   └─> No results found

2. Explore codebase:
   └─> Find: app/models/user.rb (lines 200-250)
   └─> Find: app/mailers/password_mailer.rb
   └─> Find: config/initializers/devise.rb

3. write_context(
     query_key: "password-reset",
     title: "Password reset flow using Devise",
     content: "Uses Devise's recoverable module...",
     gotchas: [...],
     related: ["email-delivery", "devise-auth"]
   )
```

**Case B: Existing context found (check freshness!)**
```
Task: "Add password reset functionality"

1. search_context(query: "password reset")
   └─> Found: ctx_abc123 "Devise auth" (updated 2 weeks ago)

2. read_context(id: "ctx_abc123")
   └─> Content is old - doesn't mention password reset
   └─> version: 1, updated_at: 2026-02-15

3. get_context_versions(id: "ctx_abc123")
   └─> No other versions - this is the original

4. update_context(
     id: "ctx_abc123",
     content: "Devise auth (UPDATED with password reset):
       - Uses recoverable module for password resets
       - Token: reset_password_token (unique string)
       - Timestamp: reset_password_sent_at
       - PasswordMailer sends reset emails
       - Token valid for 6 hours",
     gotchas: [
       "Token expires after 6 hours - no way to extend",
       "No rate limiting on reset requests (NEW)"
     ],
     change_note: "Added password reset info from feature work"
   )
```

---

## Skill: winnow-compact

**Purpose:** Compress context before continuing work or ending a session

**When to use:**
- After 15-20 minutes of continuous work
- Before starting a new phase of work
- Before ending a session (save for future agents)
- When entering the "dumb zone" (context >40%)

**Philosophy:**
Context compaction resets the agent's state while preserving learning. It's the antidote to the dumb zone.

### Workflow

```
1. IDENTIFY what you've learned
   └─> Query the topic(s) you've been working on
   
2. FETCH all related chunks
   └─> Call compact_context with query to get all matching chunks in one call
   
3. SUMMARIZE (agent-side — you do this, not the server)
   └─> Produce a structured summary:
       - What: one-liner description
       - Files: key file paths + line numbers
       - Gotchas: warnings from all chunks
       - Related: connected context areas
       - Gaps: what's still unknown
   
4. WRITE compacted summary back
   └─> Call write_context with the summary as a new chunk
   └─> Use a descriptive query_key (e.g., "auth-compacted-2026-03")
   
5. DECIDE next step:
   a) Start fresh session with summary as seed
   b) Continue working with compressed context
```

### Context Compaction Checklist

Before compacting, confirm:
- [ ] Can describe what you've learned in 2-3 sentences
- [ ] Listed key files with line numbers
- [ ] Identified gotchas for future agents
- [ ] Noted related areas that weren't explored

### Example

```
Agent has been working for 20 minutes on auth refactoring.

1. compact_context(query: "auth session")
   └─> Returns 8 raw chunks about auth

2. Agent summarizes (in its own context):
   "Session handling via Warden with database strategy.
    Keys: session_key, user_id. Expires 30d.
    Files: app/models/user.rb (120-180), app/warden/strategies/db.rb (1-50)
    Gotchas: No remember_token, Cleanup daily 3am UTC
    Related: api-auth
    Gaps: OAuth2 support undocumented"

3. Write compacted summary back:
   write_context(
     query_key: "auth-session-compacted-2026-03",
     title: "Auth session — compacted summary (Mar 2026)",
     content: "Session handling via Warden with database strategy...",
     gotchas: ["No remember_token", "Cleanup daily 3am UTC"],
     related: ["api-auth"]
   )
```

---

## Skill: winnow-onboard

**Purpose:** Quickly get a new agent up to speed on a codebase area

**When to use:**
- Agent starts fresh and needs to understand context
- Hand-off between agents
- Returning to a codebase after time away

**Philosophy:**
Onboarding should be fast, focused, and compressed. Start with the highest-level context, then drill down as needed.

### Workflow

```
1. QUERY major subsystems
   └─> "What are the major areas of this codebase?"

2. READ top context for each subsystem
   └─> Prioritize most relevant to current task
   
3. BUILD mental model
   └─> Map relationships between areas
   └─> Identify where current task fits
   
4. IDENTIFY gaps
   └─> What don't we know yet?
   └─> Write context if needed (future agents thank you)
```

### Example

```
New agent starting work on payments.

1. search_context(query: "payment", limit: 20)
   └─> Returns: payment-tokens, stripe-integration, refund-flow

2. Read top 2 results:
   └─> read_context(id: "ctx_payment_tokens")
   └─> read_context(id: "ctx_stripe_integration")

3. Mental model:
   └─> Payments use Stripe
   └─> Tokens stored in FormOfPayment
   └─> Refunds go through different flow
   
4. Gap: "What's the retry logic for failed payments?"
   └─> write_context(query: "payment-retry", ...)
```

---

## Skill: winnow-plan

**Purpose:** Create implementation plans that can be reviewed and executed reliably

**When to use:**
- After research is complete
- Before writing any code
- When handoff to another agent

**Philosophy:**
A good plan is compression of intent. Even weaker models can execute good plans reliably.

### Workflow

```
1. REVIEW research
   └─> Read all relevant context chunks
   
2. STRUCTURE plan
   └─> File names with paths
   └─> Line numbers for key changes
   └─> Code snippets for patterns to follow
   └─> Test approach
   
3. VALIDATE
   └─> Can you explain this plan in 3 sentences?
   └─> Does it reference specific code locations?
   └─> Are there clear success criteria?
   
4. WRITE plan as context
   └─> Store for review and execution
```

### Plan Template

```markdown
## Plan: [Feature Name]

### What
[One sentence description]

### Files to modify
- `app/models/user.rb` (lines 100-150): Add method
- `app/controllers/api/v1/users_controller.rb` (lines 50-75): Add endpoint

### Code patterns to follow
- See `app/models/session.rb` lines 30-40 for similar pattern

###}
---

## Skill: winnow-review

**Purpose:** Validate and improve context quality through structured reviews

**When to use:**
- After completing a task that used context
- When user provides feedback on agent generation
- During periodic quality audits
- When context seems outdated or incorrect

**Philosophy:**
Context quality improves through feedback loops. Following the original development MCP's `add_doc_review` pattern, agents should review context after use.

### Workflow

```
1. IDENTIFY context used
   └─> What context chunks helped (or hurt) the task?
   
2. ASSESS quality
   └─> Usefulness: Did this help complete the task?
   └─> Correctness: Is the information accurate?
   
3. DETERMINE action
   └─> useful: Keep as-is
   └─> needs_update: Review and update
   └─> outdated: Mark for refresh
   └─> incorrect: Flag for correction
   
4. WRITE review
   └─> Call review_context with ratings and notes
   
5. IF action != useful
   └─> Update or rewrite context chunk
```

### Review Quality Checklist

Before submitting a review, confirm:
- [ ] Used this context to complete a task
- [ ] Rated usefulness (1-5) with note explaining why
- [ ] Rated correctness (1-5) with note if issues found
- [ ] Selected appropriate action based on quality

### Example

```
Agent just completed adding password reset functionality using context about auth.

1. Which context helped?
   └─> ctx_abc123: "Session-based auth with Warden"

2. Assess quality:
   └─> Usefulness: 4 - Gotchas about token expiry were helpful
   └─> Correctness: 5 - All info was accurate

3. Action: useful

4. review_context(
     chunk_id: "ctx_abc123",
     task: "Added password reset functionality",
     usefulness: 4,
     usefulness_note: "Gotchas about token expiry were very helpful",
     correctness: 5,
     action: "useful"
   )

5. No update needed - context is good.
```

---

*Last updated: 2026-03-08*
*Status: Draft / Iterating*
