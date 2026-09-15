---
id: req-a119
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - clo set cloche.something fails
    - can't override cloche.branch
    - KV write to a cloche.* key rejected
    - reserved KV namespace
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: e3bn-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461649307Z
updated: 2026-09-15T00:41:34.461649307Z
---

The cloche.* KV namespace (cloche.task_id, cloche.branch, cloche.worktree, cloche.base_sha, etc.) is read-only — both cloche set and clo set reject writes to it.

**Why:** These keys are daemon-published run/branch metadata; allowing overwrite would corrupt orchestration state.
