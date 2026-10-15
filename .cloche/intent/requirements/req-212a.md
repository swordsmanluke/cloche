---
id: req-212a
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - security-safety
hints:
    - is it safe to install a shared module
    - module update overwrote my local edits
    - how do I add a Jira module
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-10-13-modules-design.md#Distribution
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.454079061Z
updated: 2026-10-15T15:54:45.454079061Z
---

Nothing in a module runs at install or update time. `cloche module add` vendors git modules (pinned to a commit in .cloche/modules.lock) into .cloche/modules/<name>/ and commits them; `update` shows the full diff of scripts and prompts and waits for confirmation. Cloche does not install a module's container prerequisites; the module README documents them.

**Why:** Pin-and-review: module scripts are untrusted third-party code that must be reviewed in diffs before they can run, consistent with reviewing agent output.
