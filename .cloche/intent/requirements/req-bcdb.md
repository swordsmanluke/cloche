---
id: req-bcdb
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - overlay panel shows even though hidden attribute is set
    - console modal renders on page load unexpectedly
    - hidden attribute not working with display flex
    - toggling a panel with the hidden attribute doesn't hide it
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 54c32bd
    extracted_at: 2026-09-15T17:47:23Z
    extracted_by: intent-scan
created: 2026-09-15T17:48:49.61464267Z
updated: 2026-09-15T17:48:49.61464267Z
---

Console overlays (help, activity, ledger, secondary view) are shown/hidden via the HTML `hidden` attribute; any class rule that sets `display` on an overlay must be paired with `[hidden] { display: none !important; }`, since a class's `display: flex`/`grid` otherwise beats the browser's default `[hidden]` styling and the overlay renders even when marked hidden.

**Why:** This exact bug shipped: overlay class rules set display: flex, which beat [hidden]'s default display: none, so every overlay rendered on load and on close instead of toggling.
