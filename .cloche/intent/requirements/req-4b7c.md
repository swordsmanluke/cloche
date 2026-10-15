---
id: req-4b7c
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - script in module cannot find file
    - workflow name collision with module
    - validate fails module has no README
    - calling a private module workflow
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/modules.md#Module layout
    extracted_at: 2026-10-15T16:29:16.293000826Z
    extracted_by: intent-scan
created: 2026-10-15T16:29:16.339006808Z
updated: 2026-10-15T16:29:16.339006808Z
---

Every module needs a README.md (Provides, Inputs and configuration, Prerequisites, Credentials), and module scripts locate their own files via CLOCHE_MODULE_DIR rather than project-relative paths. Module workflows not listed in module.toml exports are private.

**Why:** Keeps module names from colliding with project workflows and makes modules relocatable between host and container.
