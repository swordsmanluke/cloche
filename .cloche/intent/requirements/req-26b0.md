---
id: req-26b0
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - cloche stop did not stop the container
    - cancelled run still running
    - stop a task from the loop
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 8647a01939660ad08d4ea5e75c377210ca1cc6f9
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.455374098Z
updated: 2026-10-15T15:54:45.455374098Z
---

Loop-dispatched `main` runs must be registered under a cancellable context keyed by attempt ID so `cloche stop` really cancels the engine and stops the attempt's containers; setting the run to cancelled in the store alone is not a stop.

**Why:** StopRun only handled runs with a container ID or host cancel func; loop-dispatched runs had neither and kept running after being marked cancelled.
