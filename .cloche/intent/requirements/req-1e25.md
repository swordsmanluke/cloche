---
id: req-1e25
status: active
scope:
    level: domain
    domains:
        - security-safety
        - daemon-host-orchestration
hints:
    - can a long-lived agent edit code
    - what may a DRA do
    - agent session lost its memory
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/2026-11-10-autonomous-loop-design.md#Part 5: DRAs (Domain Responsible Agents)'
    extracted_at: 2026-11-11T00:16:38.456159878Z
    extracted_by: intent-scan
created: 2026-11-11T00:16:38.513330977Z
updated: 2026-11-11T00:16:38.513330977Z
---

A DRA (Domain Responsible Agent) manages work for its domain but never changes code, stops or deletes runs; it runs in a locked-down container with its own image, read-only project mount apart from its notes.md, and reaches the tracker only through the daemon. Its session is disposable and notes.md is the memory.

**Why:** Long-lived agents are the riskiest kind; code changes must go through tasks and runs for container, tests and review.
