---
id: req-c377
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
        - container-runtime
hints:
    - tests ran against wrong commit
    - why was the check round skipped
    - signal post failed what happens
confidence: low
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#Signal sources shipped with cloche
    extracted_at: 2026-11-11T01:55:43.654771223Z
    extracted_by: intent-scan
created: 2026-11-11T01:55:43.708976921Z
updated: 2026-11-11T01:55:43.708976921Z
---

check-main only tests a clean checkout of `main`, and a red HEAD is recorded as checked only after the signal post succeeds so failed posts are retried; the container copies the project root, so a dirty or non-main root would not test main's HEAD.

**Why:** Container runs copy the whole project root, so results are only meaningful for main when the root is a clean main checkout.
