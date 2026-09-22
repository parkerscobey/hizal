---
name: hizal-register-focus
description: Declare what the agent is currently working on. Self-triggering — fires whenever the agent has a clear task and wants related context to auto-inject. Use at session start when the task is known, or mid-session when the task changes. Triggers on phrases like "here's what I'm doing", "my task is", "I'm working on", "let me focus on", or any time the agent articulates a specific goal or ticket.
---

# Hizal Register Focus

Declare what you're working on. Enables focus-tag-based chunk injection.

## Usage

```
hizal__register_focus(
  session_id="<session-id>",
  task="<clear description of current work>",
  tags=["<relevant-tag>", "<another-tag>"]
)
```

## Parameters

- **session_id** — the active session's ID (from `start_session` or `get_active_session`)
- **task** — a short, clear description of what you're doing (e.g., "Implement webhook signature verification for Nuvei DMNs")
- **tags** — keywords that match `focus_tags` rules on context chunks. Chunks with matching rules get injected into your session automatically.

## When to Use

- At session start, if the task is known
- Mid-session, if the task changes significantly
- After a context reset, to re-establish focus

## Response

Returns `focus_chunks` — every focus-tag-matched chunk (newly added first) with
`id, query_key, title, scope, chunk_type`. Pull full content via `read_context`.
`focus_injected_chunks` is the total matched; `0` truly means nothing matched
(already-injected matches count). `focus_new_chunks` counts newly added ones.

## Tips

- Keep tags specific and domain-relevant. Good: `["webhooks", "signing", "nuvei"]`. Bad: `["code", "work"]`.
- You can re-register with different tags as the task evolves — it replaces the previous focus.
