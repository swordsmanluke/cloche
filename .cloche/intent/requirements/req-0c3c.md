---
id: req-0c3c
status: active
scope:
    level: domain
    domains:
        - cli
hints:
    - cloche init --new committed files without me asking
    - why did init create a git commit automatically
    - my scaffold files aren't showing up inside the container
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#cloche-init
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.456231385Z
updated: 2026-09-14T15:33:28.456231385Z
---

`cloche init --new` finishes by automatically committing the generated scaffold (unless `--no-commit` or staged changes are present), because containers are seeded from a clean git snapshot of the last commit — uncommitted files are invisible in-container.

**Why:** Without the auto-commit, a freshly scaffolded project's workflow files, Dockerfile, and prompts would silently not exist inside the very containers they configure, since container seeding pins to a committed SHA.
