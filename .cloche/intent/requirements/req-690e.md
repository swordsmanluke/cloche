---
id: req-690e
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - workflow-dsl
hints:
    - swap Beads for Jira
    - task claim failed because someone else took it
    - where does the task get claimed
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/2026-10-13-modules-design.md#Standard contract: task management'
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.454217904Z
updated: 2026-10-15T15:54:45.454217904Z
---

The task-tracking contract is list-tasks (writes tasks_path), claim-task (new phase run by the daemon before main; a lost claim ends in fail with claim_status = unavailable, which the daemon treats as skip-for-dedup-window, not an error), main (project-owned, no longer claims), close-task and release-task. Fallbacks for stdout-parsed list-tasks and main-claims-itself remain until a future minor release removes them.

**Why:** Makes the task tracker swappable (Beads, Jira) without rewriting the orchestration loop while keeping existing projects working during the transition.
