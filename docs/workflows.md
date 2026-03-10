# Winnow Agent Workflows

Practical patterns for using Winnow in real coding sessions.

---

## Workflow 1: Research Phase (RPI)

The **Research → Plan → Implement** pattern. Use Winnow to accumulate knowledge during research, then compact before you start writing code.

```
┌─────────────────────────────────────────────────────────┐
│                    RESEARCH PHASE                        │
│                                                          │
│  Read code → write_context(findings)                     │
│  Read tests → write_context(test patterns)               │
│  Read docs  → write_context(integration details)         │
│                                                          │
│  Repeat until you understand the area                    │
└─────────────────────────────────────────────────────────┘
              ↓
┌─────────────────────────────────────────────────────────┐
│                    COMPACT                               │
│                                                          │
│  compact_context("feature area")                         │
│  → summarize all chunks into one                         │
│  → write_context(summary)                                │
│  → delete source chunks                                  │
│                                                          │
│  Now your context window is clean                        │
└─────────────────────────────────────────────────────────┘
              ↓
┌─────────────────────────────────────────────────────────┐
│                    PLAN + IMPLEMENT                      │
│                                                          │
│  search_context("what I need to know")                   │
│  → retrieve compact summary                              │
│  → implement with full context, clean window             │
└─────────────────────────────────────────────────────────┘
```

**Step by step:**

```
# During research
write_context(
  query_key="stripe-integration",
  title="Stripe customer creation flow",
  content="Customers are created in internal/billing/customer.go. The CreateCustomer function...",
  source_file="internal/billing/customer.go",
  source_lines=[15, 45]
)

write_context(
  query_key="stripe-integration",
  title="Stripe webhook handling",
  content="Webhooks hit POST /webhooks/stripe. Signature verified using...",
  gotchas=["Must use raw body for sig verification"]
)

# ... 10-15 more chunks later, context is filling up

# Compact before implementing
compact_context(query="stripe integration", limit=15)
# → receive all 15 chunks

# Write a summary chunk
write_context(
  query_key="stripe-integration",
  title="COMPACT: Full Stripe integration overview",
  content="[Summary of all 15 chunks: customer creation, webhooks, subscriptions, error handling...]"
)

# Now implement with a fresh context window
search_context(query="stripe integration", limit=1)
# → returns the compact summary
```

---

## Workflow 2: Onboarding a New Codebase

When you're dropped into an unfamiliar repo, use Winnow to build a knowledge base as you explore.

```
# Start with high-level structure
write_context(
  query_key="codebase-overview",
  title="Repository structure",
  content="Monorepo with packages: api/ (Go), web/ (React), infra/ (Terraform). Main entry: cmd/server/main.go"
)

# Document each major subsystem as you find them
write_context(
  query_key="codebase-overview",
  title="Database layer",
  content="Uses pgx directly (no ORM). All queries in internal/db/. Schema in migrations/.",
  source_file="internal/db/queries.go"
)

write_context(
  query_key="codebase-overview", 
  title="Auth system",
  content="JWT-based. Tokens issued at POST /auth/login, refreshed at POST /auth/refresh. 15min access token, 7d refresh.",
  source_file="internal/auth/",
  gotchas=["Refresh tokens are single-use (rotated)"]
)

# After exploring, search to find context gaps
search_context(query="error handling patterns", query_key="codebase-overview")
# → if nothing found, go explore that area

# Review what you've accumulated
compact_context(query="codebase overview")
# → audit what you know, identify gaps
```

---

## Workflow 3: Bug Investigation

Use Winnow to track your investigation trail so you don't re-investigate the same dead ends.

```
# Document what you've ruled out
write_context(
  query_key="bug-#4521",
  title="RULED OUT: Not a race condition",
  content="Added mutex to the suspected area, bug still reproduces. Not a concurrency issue.",
  gotchas=["Don't revisit this — already ruled out"]
)

write_context(
  query_key="bug-#4521",
  title="Reproduction: happens when user has >1000 items",
  content="Consistently reproduced with seed data of 1001 items. Never reproduced with 999. Likely a pagination boundary issue.",
  source_file="internal/api/list.go",
  source_lines=[78, 95]
)

# When you find the root cause
update_context(
  id="<initial-hypothesis-chunk-id>",
  content="ROOT CAUSE FOUND: Off-by-one in pagination. Line 82 uses `>` instead of `>=` for the boundary check.",
  change_note="Found root cause"
)

# After fix is merged, review the context
review_context(
  chunk_id="<root-cause-chunk-id>",
  task="Fixing bug #4521",
  usefulness=5,
  correctness=5,
  action="useful"
)
```

---

## Workflow 4: Context Compaction Mid-Task

When your context window hits ~40-50%, compact to reset — without losing what you've learned.

```
# Check what you have
compact_context(query="current task area", limit=20)

# Agent summarizes all chunks:
# "Here's everything we know so far about the payment flow:
#  1. Customer creation happens in billing/customer.go
#  2. Webhook verification uses HMAC-SHA256 with raw body
#  3. ..."

# Write the summary
write_context(
  query_key="payments-refactor",
  title="COMPACT: Payment flow knowledge base (session 1)",
  content="[The summary written by the agent]",
  gotchas=["Compact of 12 chunks from research session 1"]
)

# Now you can continue with a clean context
# On next session or after compaction, retrieve with:
search_context(query="payment flow", limit=3)
```

---

## Workflow 5: Cross-Session Continuity

Winnow makes context persistent across agent sessions. Use it as your handoff mechanism.

**End of session:**
```
write_context(
  query_key="feature-oauth",
  title="SESSION HANDOFF: OAuth implementation status",
  content="Completed: Google OAuth flow (PR #234). In progress: GitHub OAuth — auth code exchange working but token storage not implemented yet. Next: implement token refresh in internal/auth/oauth.go line 156.",
  gotchas=["GitHub scope must include 'user:email' — easy to miss"]
)
```

**Start of next session:**
```
search_context(query="OAuth implementation status current")
# → immediately picks up where you left off
```

---

## Tips for All Workflows

**Write chunks early and often.** It's cheaper to write a chunk you don't end up needing than to lose a finding and re-discover it.

**Use `query_key` as a project/feature namespace.** Don't use a single global namespace — search within `"feature-payments"` is much more precise than searching everything.

**Title your chunks like it's a changelog.** `"Webhook signature validation requires raw body"` is better than `"Webhooks"`. Someone (you, in 3 sessions) needs to scan the title and know what's inside.

**Compact before implementing, not during.** You want your full context window available when writing code. Compact at the end of research, before you start building.

**Review after merging.** A quick review takes 10 seconds and makes the knowledge base better for the next task.
