---
id: req-43d3
status: active
scope:
    level: domain
    domains:
        - project-config
        - intent-continuity
hints:
    - config.toml setting has no effect
    - do I need to restart the daemon after editing config
    - scan_after_tasks false ignored
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: bd9f944740903bb033431b3a21bfb9d33af53962
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.455519733Z
updated: 2026-10-15T15:54:45.455519733Z
---

Config keys placed in the wrong table parse cleanly but are silently ignored (e.g. scan_after_tasks outside [intent]); surface such undecoded keys via `cloche validate`, `cloche doctor`, and the daemon log rather than ignoring them. Per-trigger settings such as intent.scan_after_tasks are re-read from config.toml on every trigger, not cached at daemon start.

**Why:** A misplaced opt-out silently left the default in force, and editing config.toml had no effect until a daemon restart.
