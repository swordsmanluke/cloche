---
id: req-8ebe
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - how does the daemon know a task is urgent
    - task jumps the queue
    - custom task tracker priority field
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: b5f4b8871f75636a6a70c5e5a4c60bd6e5777fe7
    extracted_at: 2026-11-11T01:53:42.865054629Z
    extracted_by: intent-scan
created: 2026-11-11T01:53:42.919188315Z
updated: 2026-11-11T01:53:42.919188315Z
---

A task is urgent when its list-tasks entry has priority 0 or an `urgent` label; `create-task` adds the `urgent` label for priority 0. Custom list-tasks workflows must emit `priority` and `labels` for the daemon to spot urgent tasks.

**Why:** The daemon uses this to decide emergency-slot launches; beads-tasks and github-tasks already follow the contract.
