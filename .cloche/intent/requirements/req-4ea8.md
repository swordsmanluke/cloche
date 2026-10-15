---
id: req-4ea8
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - workflow-dsl
hints:
    - switch task tracker
    - use GitHub Issues instead of Beads
    - where is close-task defined
    - module provides a workflow phase
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: k501-main
    extracted_at: 2026-10-15T16:23:02.761300226Z
    extracted_by: intent-scan
created: 2026-10-15T16:23:02.806276964Z
updated: 2026-10-15T16:23:02.806276964Z
---

Daemon orchestration phases (`list-tasks`, `claim-task`, `main`) plus `release-task` and `close-task` resolve through the root binds in `project.cloche`, so a tracker module can provide them; swapping trackers is done in `project.cloche` only.

**Why:** Tracker integrations (Beads, GitHub Issues) are interchangeable modules behind one contract rather than hard-coded scripts.
