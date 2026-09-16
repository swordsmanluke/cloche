---
id: req-8f4c
status: active
scope:
    level: domain
    domains:
        - core-domain-ports
        - web-dashboard
hints:
    - waiting task not showing up in the console
    - run parked at a poll step disappearing from the UI
    - which run states count as active
    - project tab folding into More even though a poll run is active
    - adding a new run state — does it need to be treated as active
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 4a2f2b329680231d00e7ef0d7fef4af82b7fa21d
    extracted_at: 2026-09-16T19:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T18:36:03.905596761Z
updated: 2026-09-16T18:36:03.905596761Z
---

A run's `waiting` state (parked at a poll step, driven asynchronously without holding a concurrency slot) counts as an active/in-progress run everywhere in the console and orchestration code. Use `domain.IsActiveRunState`/`domain.ActiveRunStates` (pending, running, waiting) rather than a hand-rolled state list when checking if a run is in progress. `parked` is deliberately excluded — it's explicitly quiesced rather than progressing on its own, and already gets its own dedicated UI treatment (header pill, NeedsYou/attention list).

**Why:** Fixed a bug where waiting runs were invisible in the task stack's Running group, the project tab bar's active count, and occupancy grouping, because various call sites only checked for pending+running. A shared helper prevents the same state-list drift from recurring as new call sites are added.
