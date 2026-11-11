---
id: req-83f8
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - daemon-host-orchestration
hints:
    - how do I run a workflow on a schedule
    - cron-like trigger in cloche
    - run something every few hours
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 42d8d6aa194f208da6d70c1397bfe08988b49939
    extracted_at: 2026-11-11T01:55:43.654771223Z
    extracted_by: intent-scan
created: 2026-11-11T01:55:43.709083923Z
updated: 2026-11-11T01:55:43.709083923Z
---

Cloche has no time-trigger mechanism yet (CLOCHE-A114); periodic workflows emulate a timer with a `poll` wait step comparing the clock to a target timestamp set by a schedule step.

**Why:** Workaround until a real time trigger exists; intervals are approximate.
