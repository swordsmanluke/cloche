---
id: req-9f46
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - how do I swap one workflow for another
    - which review workflow gets called
    - module workflow not found by name
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-10-13-modules-design.md#Binds
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.453823649Z
updated: 2026-10-15T15:54:45.453823649Z
---

Module workflows are reachable only through a bind; an unbound slot resolves to a project workflow of that name, then to a built-in. Binds are dynamic (the innermost bind wins, reaching into sub-workflows); `bind!` overrides all binds beneath it, and only `.cloche/project.cloche` can hold project-root binds.

**Why:** Lets a module make its own internal choices while the project can still force an override, and keeps swapping an implementation to a one-line change.
