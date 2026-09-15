---
id: req-534a
status: active
scope:
    level: domain
    domains:
        - project-config
hints:
    - should agent commits use my personal git identity
    - how to tell apart human vs agent commits in git log
    - setting up git identity for cloche
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#git
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462970075Z
updated: 2026-09-15T00:41:34.462970075Z
---

The [git] bot identity (name/email/ssh_key) should be a distinct bot identity — e.g. a dedicated GitHub bot account or a users.noreply.github.com email — not the maintainer's own identity, so agent-authored commits stay separable and reviewable as external contributions.

**Why:** Explicitly stated rationale in the config reference, beyond just describing the default value.
