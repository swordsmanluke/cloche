---
id: req-51a3
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - unclaim script stopped the loop unexpectedly
    - unattended arm run halted after one failure
    - pilot mode task failure handling
    - arm driver budget caps never reached
    - loop stopped for human review during an automated experiment
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: e058f65bc93750785bd9cd745f86dc1eff0ce291
    extracted_at: 2026-09-16T15:18:21Z
    extracted_by: intent-scan
created: 2026-09-16T15:20:04.829779484Z
updated: 2026-09-16T15:20:04.829779484Z
---

In intent-ab pilot/unattended runs, the arm's unclaim script must leave the orchestration loop running after resetting a failed task, rather than the seed's default behavior of stopping the loop for human review.

**Why:** The seed's unclaim stops the loop per failure for human review; unattended arms must instead run to the driver's own attempt/wall-clock caps to complete without supervision.
