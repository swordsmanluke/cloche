---
id: req-dab3
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - no workflows found error
    - project.cloche only has binds
    - empty cloche file parse error
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 263be82694a5919f6432f2de9ad94ee3959496a7
    extracted_at: 2026-10-15T16:08:23.895900437Z
    extracted_by: intent-scan
created: 2026-10-15T16:08:23.991781272Z
updated: 2026-10-15T16:08:23.991781272Z
---

A project.cloche file holding only root-scope binds and no workflows is valid, so the parser accepts empty files only when the caller opts in with WithAllowEmpty. A normal workflow file with no workflows is still an error.
