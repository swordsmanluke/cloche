---
id: req-8eca
status: active
scope:
    level: domain
    domains:
        - usage-budget
        - project-config
hints:
    - where do I set the daily usage cap
    - budget in project config.toml not working
    - per-project spending limit
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#[budget]
    extracted_at: 2026-11-11T00:33:44.93352215Z
    extracted_by: intent-scan
created: 2026-11-11T00:33:44.981433087Z
updated: 2026-11-11T00:33:44.981433087Z
---

The daily usage cap ([budget]) is configured only in the global daemon config (~/.config/cloche/config), never in a project's config.toml.

**Why:** It is one cap for the whole daemon, not per project; per-window (5-hour) and per-project caps are not implemented.
