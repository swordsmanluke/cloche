---
id: req-0f67
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - where does merge-to-base merge to
    - running experimental agent work against cloche itself
    - main-haiku workflow dispatch
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/spikes/2026-09-17-intent-ab-pilot/README.md#Follow-up: recommendation tickets, worked by Haiku (2026-09-20/21)'
    extracted_at: 2026-10-15T15:57:31.706145393Z
    extracted_by: intent-scan
created: 2026-10-15T15:57:31.754373504Z
updated: 2026-10-15T15:57:31.754373504Z
---

Haiku-driven test work on Cloche's own code runs with the orchestration loop stopped and the host checkout on a dedicated non-main branch, because merge-to-base.sh merges into whatever branch the host has checked out.

**Why:** Isolation keeps a bad Haiku change to daemon/engine code from landing on main unreviewed; the main-haiku workflows are named so the loop's auto-dispatch never picks them up.
