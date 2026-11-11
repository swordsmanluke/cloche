---
id: req-8c1e
status: active
scope:
    level: domain
    domains:
        - dashboard-auth
hints:
    - cross-site request to dashboard
    - CSRF on a POST route
    - cookie settings for login
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#dashboard-token
    extracted_at: 2026-11-11T19:30:05.271827581Z
    extracted_by: intent-scan
created: 2026-11-11T19:30:05.323614563Z
updated: 2026-11-11T19:30:05.323614563Z
---

Dashboard mutating requests also pass an Origin check in addition to the token, and the token cookie is HttpOnly and SameSite=Strict.

**Why:** Prevents another site from posting to the dashboard from the user's browser.
