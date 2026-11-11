---
id: req-c551
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
        - signals
hints:
    - why isn't my ticket picked up
    - task not being run by the loop
    - label needed for task to start
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 4aa87c8df8269137d25c106afc9e466fa0059c28
    extracted_at: 2026-11-11T17:52:17.679367653Z
    extracted_by: intent-scan
created: 2026-11-11T17:52:17.745584546Z
updated: 2026-11-11T17:52:17.745584546Z
---

beads-tasks list-tasks hands out only open, non-epic tasks carrying the `ready` label; tasks created from a signal (create-task with signal_source) get `ready` automatically so they still run.

**Why:** Tracking tickets can exist without being deferred or picked up by the loop.
