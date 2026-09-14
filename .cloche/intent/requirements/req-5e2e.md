---
id: req-5e2e
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - I wrote timeout -> a-step at the workflow level and it's being ignored
    - global wire for redirecting all step timeouts
    - workflow-level timeout redirect isn't working
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/workflows.md#token-limits
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.456349217Z
updated: 2026-09-14T15:33:28.456349217Z
---

Only `token-limit -> <target>` is supported as a workflow-level global wire shorthand. A `timeout -> <target>` line at the workflow level parses without error but has no effect; redirect per-step timeouts with an explicit `<step>:timeout -> <target>` wire instead.

**Why:** The runtime only consults the global wire config for `token-limit`, not `timeout` — the DSL silently accepting the syntax without acting on it is a known gotcha worth flagging rather than a feature to rely on.
