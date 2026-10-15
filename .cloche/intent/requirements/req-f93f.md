---
id: req-f93f
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - security-safety
hints:
    - is it safe to install a module from git
    - module install running hooks
    - submodules in a module repo
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: dae21f8e6db8823fbae4d1ec4ec88a20e322032d
    extracted_at: 2026-10-15T16:16:50.445762526Z
    extracted_by: intent-scan
created: 2026-10-15T16:16:50.494343012Z
updated: 2026-10-15T16:16:50.494343012Z
---

Installing a module from a git source extracts tracked files only via `git archive`, so no git filters, hooks or submodules run during install.

**Why:** Vendoring third-party module content must not execute code from the source repository at install time.
