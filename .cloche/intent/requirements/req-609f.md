---
id: req-609f
status: active
scope:
    level: domain
    domains:
        - container-runtime
        - project-config
hints:
    - override file showing up in result branch
    - CLAUDE.md from overrides committed to branch
    - unexpected settings.json in diff
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-11-07-override-merge-design.md
    extracted_at: 2026-11-11T00:16:38.456159878Z
    extracted_by: intent-scan
created: 2026-11-11T00:16:38.512811591Z
updated: 2026-11-11T00:16:38.512811591Z
---

Files written by `.cloche/overrides/` (copied or merged) must never reach a result branch unless the agent itself edited them. Container start records a SHA-256 manifest at /etc/cloche/overrides.json (root-owned, outside /workspace); extraction restores untouched files from BaseSHA (or deletes them if absent at base) before `git add -A`, keeps files whose hash differs, and fails if the manifest exists but is unreadable.

**Why:** Previously overrides like CLAUDE.md or plugin settings were committed to cloche/<run-id> and merged into main; a silent skip would commit override content unnoticed.
