---
id: req-f3d0
status: active
scope:
    level: domain
    domains:
        - security-safety
hints:
    - I need to give the container access to a host directory
    - using CLOCHE_EXTRA_MOUNTS to share files with a container
    - mounting host secrets into a container run
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#filesystem-isolation
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454785053Z
updated: 2026-09-14T15:33:28.454785053Z
---

Avoid using `CLOCHE_EXTRA_MOUNTS` to bind-mount sensitive host directories into containers. If a container needs extra files, prefer the `.cloche/overrides/` mechanism instead.

**Why:** Cloche's filesystem isolation model deliberately gives containers a copy of the project, not a bind mount, so the agent cannot touch the host filesystem directly; bind-mounting sensitive directories defeats that guarantee.
