---
id: req-d1d4
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - Done list jumps back to the top while scrolling through history
    - console.js stack poll clearing loaded pages
    - merging polled data with paginated data in the console
    - extraDone state getting reset unexpectedly
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 819ba39ec0672f77b7d81846ea4c812bfa77f506
    extracted_at: 2026-09-16T16:05:44Z
    extracted_by: intent-scan
created: 2026-09-16T16:07:30.147119717Z
updated: 2026-09-16T16:07:30.147119717Z
---

In the console's Done group, the 4-second stack poll only ever re-fetches page one; any "earlier" pages the user has already loaded via cursor (state.extraDone) must survive that poll and only get cleared on a genuine project switch or initial load, with new completions prepended to page one.

**Why:** Discarding extraDone on every poll would silently un-page the user back to page one every 4 seconds while they're scrolled into history, defeating the point of pagination.
