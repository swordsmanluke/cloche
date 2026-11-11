---
id: req-e786
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - daemon-host-orchestration
hints:
    - passing task id into a tracker workflow gets wrong value
    - kv key task_id is overwritten
    - naming inputs for create-task or update-task
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md
    extracted_at: 2026-11-11T01:39:37.954498679Z
    extracted_by: intent-scan
created: 2026-11-11T01:39:38.009689683Z
updated: 2026-11-11T01:39:38.009689683Z
---

Tracker modules' create-task and update-task use caller key names signal_source, signal_key, target_task_id, found_task_id (never task_id) because the host executor seeds task_id with the phase's own name.

**Why:** Avoids a collision with the executor-seeded task_id key.
