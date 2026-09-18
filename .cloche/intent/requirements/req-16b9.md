---
id: req-16b9
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - Cmd+C or Ctrl+C not copying in the console
    - adding a new keyboard shortcut to the dashboard
    - browser shortcut gets hijacked by the console
    - keydown handler preventDefault
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: c26df00aef3d2aeeef6e45bec6bdfe9218e898c3
    extracted_at: 2026-09-18T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-18T21:31:56.554705471Z
updated: 2026-09-18T21:31:56.554705471Z
---

The web console's global keyboard shortcuts (j/k, w/i/c/l, r/x, d, etc.) fire only on bare keys or Shift chords; any Cmd/Ctrl/Alt chord is left un-preventDefault-ed so the browser or OS handles it, including inside open overlays (help/activity/ledger/view).

**Why:** Before this fix, Cmd/Ctrl+C/L/D/A/F/G/R/W on macOS or Windows hijacked the browser's native copy/address-bar/bookmark/select-all/find/reload/close-tab actions because the handler only checked e.key, not modifier keys. Any new single-letter shortcut must keep this modifier guard.
