---
id: req-8faa
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - cli
hints:
    - module remove deleted my directory
    - uninstall a local module
    - what does module remove delete
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: dae21f8e6db8823fbae4d1ec4ec88a20e322032d
    extracted_at: 2026-10-15T16:16:50.445762526Z
    extracted_by: intent-scan
created: 2026-10-15T16:16:50.4945058Z
updated: 2026-10-15T16:16:50.4945058Z
---

`cloche module remove` only deletes the lock entry and the vendored directory for git modules; local-path module directories are never touched.

**Why:** A local-path module is the user's own source directory, not Cloche-managed vendored content.
