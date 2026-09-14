---
id: req-faca
status: active
scope:
    level: domain
    domains:
        - container-runtime
hints:
    - I fixed the Dockerfile but resume still uses the old image
    - cloche resume not picking up my image change
    - how do I make resume reuse the exact same container instead of rebuilding
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/run-isolation/architecture.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454236517Z
updated: 2026-09-14T15:33:28.454236517Z
---

`cloche resume` rebuilds the container fresh from the project image by default (picking up Dockerfile and override fixes) and re-applies the last successful workspace snapshot. Reusing the original failed container's exact filesystem is opt-in via `--no-rebuild`.

**Why:** The old default of reusing the original container's filesystem prevented Dockerfile/image fixes from ever taking effect on resume and let stale state replay indefinitely.
