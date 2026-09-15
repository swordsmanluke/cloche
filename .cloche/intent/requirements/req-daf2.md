---
id: req-daf2
status: active
scope:
    level: domain
    domains:
        - project-config
hints:
    - which SSH key URL to show during cloche init
    - bot key GitHub setup
    - deploy key vs user key for git identity
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: clcq-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.463065405Z
updated: 2026-09-15T00:41:34.463065405Z
---

cloche init should suggest a repo-scoped GitHub deploy-key URL, not the generic user-keys URL, when generating a bot SSH key for a project that already has a GitHub origin remote.

**Why:** Deploy keys are the right choice for a bot key scoped to a single repo.
