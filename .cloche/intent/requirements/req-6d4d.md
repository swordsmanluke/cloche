---
id: req-6d4d
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - daemon-host-orchestration
hints:
    - which bind wins when two scopes bind the same slot
    - bind! vs bind precedence
    - ambiguous module workflow name
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: eoo2-main
    extracted_at: 2026-10-15T16:08:23.895900437Z
    extracted_by: intent-scan
created: 2026-10-15T16:08:23.990845314Z
updated: 2026-10-15T16:08:23.990845314Z
---

Slot resolution runs innermost-first: step, then workflow, then file, then caller frames, then project.cloche, then project workflow, then built-in. The innermost scope wins, except that bind! overrides everything beneath it (an outer bind! beats an inner one). Scope frames propagate to both host and container child runs. An ambiguous RHS is an error.
