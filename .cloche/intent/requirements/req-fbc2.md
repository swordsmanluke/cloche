---
id: req-fbc2
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - which runs get parked at the budget cap
    - parked run disappeared from status
    - how to continue a parked run
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 048e4d493f936f45ef0ff1d5081f7e5428b82cb6
    extracted_at: 2026-11-11T01:36:38.712348365Z
    extracted_by: intent-scan
created: 2026-11-11T01:36:38.770806484Z
updated: 2026-11-11T01:36:38.770806484Z
---

When the budget cap parks runs, runs waiting on a poll step are left alone, and runs parked by the cap stay listed in Needs you by id until each is resumed with `cloche resume`.

**Why:** Poll-waiting runs spend nothing; parked runs outlive the over-cap state and would otherwise be forgotten.
