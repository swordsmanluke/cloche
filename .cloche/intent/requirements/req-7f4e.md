---
id: req-7f4e
status: active
scope:
    level: domain
    domains:
        - cli
hints:
    - flaky test depends on whether a daemon happens to be running
    - test passes locally but fails in CI or a different environment
    - CLOCHE_ADDR leaking into a test
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 54f4348
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462389978Z
updated: 2026-09-15T00:41:34.462389978Z
---

Tests that could reach a live daemon via an inherited CLOCHE_ADDR must isolate it explicitly (e.g. bind-and-close a local port, t.Setenv) rather than relying on the ambient environment being clean.

**Why:** A completion test could silently pass or hang by consulting a real daemon instead of exercising the intended fallback path.
