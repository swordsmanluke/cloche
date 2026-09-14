---
id: req-8eb2
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - runs relaunched by themselves after I restarted the daemon
    - how do I safely restart cloched without runs resuming
    - cloche loop stop didn't stop in-flight runs from auto-resuming
    - safe daemon maintenance sequence
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/run-isolation/architecture.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454350042Z
updated: 2026-09-14T15:33:28.454350042Z
---

Use `cloche loop stop --hard` (not plain `cloche loop stop`) before rebuilding or restarting the daemon. Plain `stop` only halts new dispatch — runs already in flight stay resumable and auto-resume when the daemon restarts.

**Why:** `--hard` additionally parks in-flight runs, which is the only way to guarantee nothing fires automatically right after a daemon rebuild/restart.
