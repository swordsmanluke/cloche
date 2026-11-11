---
id: req-bc60
status: active
scope:
    level: domain
    domains:
        - signals
        - daemon-host-orchestration
        - self-hosting-workflows
hints:
    - main branch tests are failing
    - should a red main block the loop
    - periodic CI check on main
    - new host workflow auto-start
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 42d8d6aa194f208da6d70c1397bfe08988b49939
    extracted_at: 2026-11-11T01:55:43.654771223Z
    extracted_by: intent-scan
created: 2026-11-11T01:55:43.708855361Z
updated: 2026-11-11T01:55:43.708855361Z
---

The `check-main` timer workflow is started manually (`cloche run check-main`) and is never auto-dispatched by `cloche loop`; on a red `main` it posts an urgent `ci-main`/`main-red` signal and nothing pauses on red.

**Why:** Only list-tasks, main and release-task are auto-dispatched; a signal never stops or pauses a run, and code-changing work goes through the tracker.
