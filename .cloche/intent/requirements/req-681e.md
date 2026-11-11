---
id: req-681e
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - usage-budget
hints:
    - urgent task not launching
    - how many tasks run at once
    - emergency concurrency limit
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: year-main
    extracted_at: 2026-11-11T01:49:47.526697151Z
    extracted_by: intent-scan
created: 2026-11-11T01:49:47.590541939Z
updated: 2026-11-11T01:49:47.590541939Z
---

Emergency tasks created from urgent signals count against the concurrency limit: no new ordinary task starts until total in-flight is back under concurrency, and with emergency_overflow = false the budget cap also stops emergency launches.

**Why:** Emergency slots are an overflow allowance, not a separate pool, so ordinary work cannot starve or exceed limits.
