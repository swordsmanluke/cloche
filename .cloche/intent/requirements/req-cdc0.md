---
id: req-cdc0
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - scoping the log to a running step shows no output
    - log pane placeholder never updates while a step runs
    - step output endpoint 404s mid-run
    - should a scoped log view fetch once or filter the live stream
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: q6wl-main
    extracted_at: 2026-09-17T15:20:05Z
    extracted_by: intent-scan
created: 2026-09-17T15:20:24.89627159Z
updated: 2026-09-17T15:20:24.89627159Z
---

A dashboard feature that scopes the log view to a step (or any other live-updating subset) must filter the already-streaming lines to stay live; a one-shot fetch against the archival step-output endpoint is only for backfilling a step whose live stream has no history yet (e.g. viewing an old, already-ended attempt), and its result must be merged with the stream, never substituted for it.

**Why:** The original step-scoped log view fetched a step's archived output once and replaced the log pane with that static snapshot. The archive endpoint is only written at step completion, so scoping to a still-running step permanently showed 'No output available' instead of the live log.
