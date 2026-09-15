---
id: req-f1fb
status: active
scope:
    level: domain
    domains:
        - agent-adapters
hints:
    - step marked success but the agent did nothing
    - aborted agent step still got merged
    - missing result marker defaults to success
    - prompt adapter result classification
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: a1e98a2
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462017884Z
updated: 2026-09-15T00:41:34.462017884Z
---

An agent step with no recognizable CLOCHE_RESULT marker must default to a fail/unknown result, never success — the classifier must never let unmarked or aborted output silently reach the merge path.

**Why:** A real incident: several tickets were closed as succeeded and merged with zero implementation work because the classifier defaulted unmarked output to success when the agent had actually aborted (e.g. missing prompt file).
