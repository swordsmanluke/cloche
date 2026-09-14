---
id: req-362e
status: active
scope:
    level: project
hints:
    - should I put an API key in the Dockerfile
    - hardcoding credentials in .cloche/overrides
    - storing a database URL directly in the image
    - where do secrets go for a container workflow
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#credential-handling
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454894139Z
updated: 2026-09-14T15:33:28.454894139Z
---

Never bake secrets (API keys, tokens, passwords) into Docker images or `.cloche/overrides/`. Pass credentials via environment variables at runtime (e.g. `CLOCHE_EXTRA_ENV`) instead.

**Why:** `.cloche/overrides/` is checked into version control, and images are shared/cached artifacts — either one baking in a secret leaks it far beyond a single run.
