---
id: req-b741
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - duplicate or missing rows when paging the done list
    - designing a pagination cursor for a table with ties on the sort column
    - task stack cursor implementation
    - two tasks completed at the same instant
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 819ba39ec0672f77b7d81846ea4c812bfa77f506
    extracted_at: 2026-09-16T16:05:44Z
    extracted_by: intent-scan
created: 2026-09-16T16:07:30.146957312Z
updated: 2026-09-16T16:07:30.146957312Z
---

The task-stack Done group's pagination cursor is keyed on (completed_at, task ID), not completed_at alone.

**Why:** Multiple runs can share the exact same completion timestamp; a timestamp-only cursor would either repeat or silently drop entries in that tie cluster when paging across requests. Carrying the task key alongside the timestamp keeps a page boundary landing inside a tie deterministic and lossless.
