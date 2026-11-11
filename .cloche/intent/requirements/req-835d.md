---
id: req-835d
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - how do I report an error from my app to cloche
    - dedupe repeated failures into one task
    - create task from alert
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/2026-11-10-autonomous-loop-design.md#Part 4: Feedback intake'
    extracted_at: 2026-11-11T00:16:38.456159878Z
    extracted_by: intent-scan
created: 2026-11-11T00:16:38.513038933Z
updated: 2026-11-11T00:16:38.513038933Z
---

Feedback signals enter cloche only through an HTTP endpoint (POST /api/projects/{name}/signals, per-project bearer token) in cloche's own signal format; there is no CLI command and cloche never fetches from sources. The caller chooses the dedup `key`; cloche only compares source+key. Work needing code changes always becomes a tracker task so it goes through budget, tests and review; runs are never killed or paused for a signal.

**Why:** One interface and one work queue; cloche cannot know what 'the same problem' means for each source.
