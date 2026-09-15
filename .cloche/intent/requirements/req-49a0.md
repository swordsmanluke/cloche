---
id: req-49a0
status: active
scope:
    level: domain
    domains:
        - versioning-release
hints:
    - does touching internal/dsl automatically mean a breaking change
    - changelog draft flags something as breaking that isn't user-visible
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: .cloche/prompts/draft-changelog.md
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462854657Z
updated: 2026-09-15T00:41:34.462854657Z
---

When drafting a changelog, a commit touching a breaking-change "watchlist" path (internal/dsl/**, internal/protocol/**, docs/workflows.md, cmd/cloche/**, cmd/clo/**, .cloche/scripts|prompts|host.cloche|Dockerfile) is only a prompt to check for breaking-ness, not an automatic verdict — read the diff before flagging it as breaking.

**Why:** Prevents over-flagging every touch to these paths as user-visible breaking changes when e.g. an internal refactor preserves syntax/behavior.
