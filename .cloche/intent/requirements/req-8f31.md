---
id: req-8f31
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - task stack shows an empty header with nothing under it
    - should the Running section render when there's nothing running
    - why did the Needs you group disappear from the console
    - adding a new group to the task stack renderer
    - console-stack-row test for empty groups
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 7b0eb5fe5dc44b0d4c5a9670015a74cb975d5ce2
    extracted_at: 2026-09-16T15:35:27Z
    extracted_by: intent-scan
created: 2026-09-16T15:36:26.880884203Z
updated: 2026-09-16T15:36:26.880884203Z
---

In the web dashboard's task stack, the Needs you / Running / Queued groups are omitted entirely when empty (each header and dash placeholder disappears), reappearing on the next poll as soon as they have rows again. The Done (today) group always stays visible, with a dash placeholder when empty, since it's the paginated group users expect to keep finding in the same place.

**Why:** Avoids rendering a header over an empty dash placeholder for transient groups, while keeping the one paginated group in a stable, predictable location.
