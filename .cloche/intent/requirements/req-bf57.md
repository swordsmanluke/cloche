---
id: req-bf57
status: active
scope:
    level: domain
    domains:
        - versioning-release
hints:
    - changelog draft quotes a meaningless cloche run ... succeeded commit message
    - how to describe an automated merge commit in release notes
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: .cloche/prompts/draft-changelog.md
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462764667Z
updated: 2026-09-15T00:41:34.462764667Z
---

When drafting a changelog, commits titled "cloche run <id>-<workflow>: <workflow> (succeeded)" are auto-generated squash-merge noise — never quote the subject as a description of what shipped; read the diff instead.

**Why:** The subject line itself carries no information about what changed; only the diff does.
