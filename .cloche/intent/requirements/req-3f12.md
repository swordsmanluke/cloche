---
id: req-3f12
status: active
scope:
    level: project
hints:
    - does cloche push branches for me automatically
    - who is responsible for git push in a workflow script
    - CLOCHE_GIT_SSH_COMMAND isn't doing anything by itself
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-04-21-git-identity-design.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.455316246Z
updated: 2026-09-14T15:33:28.455316246Z
---

Cloche itself never runs `git push`. The `CLOCHE_GIT_SSH_COMMAND` / `CLOCHE_GIT_AUTHOR_NAME` / `CLOCHE_GIT_AUTHOR_EMAIL` env vars are a convention that workflow scripts opt into (`GIT_SSH_COMMAND="$CLOCHE_GIT_SSH_COMMAND" git push …`); the daemon composes and injects them but never invokes the push itself.

**Why:** Keeps the push action and its consequences (which branch, which remote, when) under the user's own script logic rather than an implicit daemon behavior.
