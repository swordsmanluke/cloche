---
id: req-8fdf
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - saga poll exited 2
    - saga answer exit code 3
    - is a nonzero saga exit a failure
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: 8mfg-main
    extracted_at: 2026-11-11T21:04:22.815792323Z
    extracted_by: intent-scan
created: 2026-11-11T21:04:22.868772705Z
updated: 2026-11-11T21:04:22.868772705Z
---

`saga poll` exit code 2 means questions are still open (a waiting result, not an error) and exit 1 is failure; `saga answer --judge` / `saga hold` exit 3 means the question already has a ruling and is fine, not a failure.

**Why:** Treating these exit codes as errors breaks the ask/poll/judge flow.
