---
id: req-515d
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - status command is slow
    - budget check blocking launch
    - why does the cap lag behind actual usage
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 048e4d493f936f45ef0ff1d5081f7e5428b82cb6
    extracted_at: 2026-11-11T01:36:38.712348365Z
    extracted_by: intent-scan
created: 2026-11-11T01:36:38.770410264Z
updated: 2026-11-11T01:36:38.770410264Z
---

The budget gate never reads usage in the launch or status path: launch decisions and `cloche status` use the last reading taken by the background loop (every two minutes), so the gate can lag by up to two minutes.

**Why:** A usage read runs `claude -p /usage` and takes seconds; loops and `cloche status` must not wait on it.
