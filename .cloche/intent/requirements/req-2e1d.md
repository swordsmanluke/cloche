---
id: req-2e1d
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - will the loop pick up my new workflow automatically
    - experimental workflow getting run unexpectedly by the loop
    - why isn't my custom main variant being dispatched
    - naming a workflow so cloche loop ignores it
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: dcd8d3aa029ae2ce04f710853969587d423564b5
    extracted_at: 2026-09-16T20:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:58:52.347562973Z
updated: 2026-09-16T19:58:52.347562973Z
---

The `cloche loop` orchestration loop only ever auto-dispatches host workflows literally named `list-tasks`, `main`, or `release-task`; any other workflow name (e.g. an experimental `main-opencode`) sits inert until run explicitly with `cloche run <name>`.

**Why:** This lets an experimental or alternate pipeline (e.g. comparing agent executors) live in the same project and reuse its real Dockerfile/config without being picked up by the automatic loop or interfering with the real `main` pipeline.
