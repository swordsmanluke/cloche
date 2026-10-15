---
id: req-22b4
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - daemon-host-orchestration
hints:
    - module.toml is malformed and runs stop working
    - slot not resolving to a module workflow
    - modules failed to load
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: fd03f0959be0ee688a49c06dddad493d51274390
    extracted_at: 2026-10-15T16:12:51.719374649Z
    extracted_by: intent-scan
created: 2026-10-15T16:12:51.775888775Z
updated: 2026-10-15T16:12:51.775888775Z
---

A module registry load failure must not fail the run: log it and fall back to a nil registry so workflow slots resolve to project workflows and built-ins only.

**Why:** Broken or missing module metadata should not take down projects that do not use modules.
