---
id: req-ad04
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - which usage percentage counts toward the cap
    - per-model weekly limit hit
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 048e4d493f936f45ef0ff1d5081f7e5428b82cb6
    extracted_at: 2026-11-11T01:36:38.712348365Z
    extracted_by: intent-scan
created: 2026-11-11T01:36:38.770641181Z
updated: 2026-11-11T01:36:38.770641181Z
---

Budget usage is the fullest weekly window reported by `/usage`, not only the all-models one.

**Why:** A per-model weekly window can run out before the all-models window does.
