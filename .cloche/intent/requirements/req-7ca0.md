---
id: req-7ca0
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - two intent scans running at the same time
    - does completing multiple tasks queue multiple scans
    - intent scan concurrency behavior
    - post-task auto scan seems to get skipped
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#Incremental-scans-on-task-completion
    extracted_at: 2026-09-16T19:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T18:36:03.905758277Z
updated: 2026-09-16T18:36:03.905758277Z
---

A project is never scanned by `intent-scan` twice in parallel — if a scan is already queued or running, a new trigger (e.g. the automatic post-task scan) is skipped rather than enqueuing a duplicate.

**Why:** Incremental scans are enqueued automatically after every completed main task; without this guard a burst of task completions would queue redundant concurrent scans.
