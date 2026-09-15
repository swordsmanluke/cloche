---
id: req-058a
status: active
scope:
    level: domain
    domains:
        - persistence
hints:
    - clo set value too large
    - KV store rejects a large payload
    - where to put intermediate step output
    - temp_file_dir usage
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: pwey-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.463170755Z
updated: 2026-09-15T00:41:34.463170755Z
---

KV store values are capped at 1KB; anything larger (structured agent outputs, review feedback, generated content) must go through the temp_file_dir scratch directory instead, and must not be committed to git.

**Why:** Standardizes a scratch-file convention so projects don't invent ad hoc temp-file paths and gitignore patterns for large step output.
