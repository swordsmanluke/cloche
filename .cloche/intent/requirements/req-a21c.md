---
id: req-a21c
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - task stack group disappearing or always showing
    - should this dashboard group hide when empty
    - Needs you / Queued / Running / Done visibility rules
    - why did the Needs you group disappear from the console
    - should the Running section render when there's nothing running
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 74e57ce
    extracted_at: 2026-09-18T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-18T21:31:56.555092011Z
updated: 2026-09-18T21:31:56.555092011Z
---

In the web dashboard's task stack, the Needs you and Queued groups are omitted entirely when empty (reappearing on the next poll as soon as they have rows again). The Running and Done groups always stay visible, each with a dash placeholder (or a `0` count for Running) when empty — Running as an always-on "nothing running" signal, Done since it's the paginated group users expect to keep finding in the same place.

**Why:** v3.24.11 commit 74e57ce changed Running from 'hidden when empty' to 'always visible with a 0 count', which directly contradicts req-8f31's statement that Running is omitted when empty. req-8f31 was extracted from the pre-74e57ce behavior (commit 7b0eb5f, v3.24.0) and is now factually wrong.
