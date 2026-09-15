---
id: req-010f
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - why did a project's tab disappear into the More menu
    - changing how the dashboard tab bar decides what to fold
    - a project with a stopped loop is hiding other projects' tabs
    - tab bar overflow behavior
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/web-dashboard.md#tab-bar
    extracted_at: 2026-09-15T17:55:00Z
    extracted_by: intent-scan
created: 2026-09-15T17:54:46.76723112Z
updated: 2026-09-15T17:54:46.76723112Z
---

The console's tab bar fold rule always keeps the active project and any project with live activity (running loop, active runs, or attention items) visible; remaining slots fill with the most recently active projects, and only genuinely stale/inactive projects beyond that budget fold into the "More" menu.

**Why:** Prevents a stopped loop with no current activity from folding every other project down to a single visible tab — a naive recency-only or fixed-slot fold rule would do that.
