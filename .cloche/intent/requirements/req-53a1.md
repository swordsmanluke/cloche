---
id: req-53a1
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - intent scan not running automatically
    - scan skipped over budget
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 048e4d493f936f45ef0ff1d5081f7e5428b82cb6
    extracted_at: 2026-11-11T01:36:38.712348365Z
    extracted_by: intent-scan
created: 2026-11-11T01:36:38.770967298Z
updated: 2026-11-11T01:36:38.770967298Z
---

The post-task intent scan is held back while the daily budget cap is exceeded.

**Why:** Automatic background scans spend model usage and must respect the cap like other launches.
