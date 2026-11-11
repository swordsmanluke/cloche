---
id: req-e828
status: active
scope:
    level: domain
    domains:
        - signals
        - daemon-host-orchestration
hints:
    - task keeps failing over and over
    - duplicate tasks created for the same failing step
    - when does a failed run become a signal
    - repeat failure threshold
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#Signal sources shipped with cloche
    extracted_at: 2026-11-11T01:55:43.654771223Z
    extracted_by: intent-scan
created: 2026-11-11T01:55:43.708676121Z
updated: 2026-11-11T01:55:43.708676121Z
---

When a task's failed attempt reaches the repeat-failure threshold (`[attention] repeat_failure_threshold`, default 3), the daemon posts a `cloche` source signal with severity high and key `<task id>:<failed step>`; only the attempt that reaches the threshold signals, and a single failed run is never a signal.

**Why:** Prevents repeated signals/tasks for every further failure; handle-notification dedupes on the key by commenting on the open task.
