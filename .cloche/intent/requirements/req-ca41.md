---
id: req-ca41
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - why does the running task's elapsed time reset after a retry
    - adding a new elapsed/duration field to the console
    - task stack shows a small elapsed time but the task has been retrying for hours
    - facts row and running row elapsed time disagree
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 5c5b88fa4bd06bd15e18bcfe1f318e8bfd312648
    extracted_at: 2026-09-18T21:40:00Z
    extracted_by: intent-scan
created: 2026-09-18T21:38:16.74130691Z
updated: 2026-09-18T21:38:16.74130691Z
---

In the web dashboard, any elapsed-time display for a running task shows time scoped to the current retry/current unit of work (the most recent of the run's own start, its most recently re-dispatched active child run's start, and the currently executing step's start) as the primary figure, with the cumulative time since the task's very first attempt kept as a secondary figure (e.g. a tooltip) rather than dropped.

**Why:** Without this, a retried task's elapsed time either grows across the whole task (misleadingly implying the current attempt has been running that whole time) or the useful cumulative total disappears entirely. The convention keeps 'time in this attempt' as the headline number both in the task stack's Running row and the centre pane's facts row, while still surfacing the full history.
