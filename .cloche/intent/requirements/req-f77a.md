---
id: req-f77a
status: active
scope:
    level: project
hints:
    - can I auto-merge the cloche result branch without looking at it
    - is it safe to skip reviewing agent changes before merge
    - trusting agent output without review
    - what should I check before merging an agent's branch
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#review-before-merge
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.455112271Z
updated: 2026-09-14T15:33:28.455112271Z
---

Always review an agent's changes before merging. Cloche extracts results to git branches, never directly to the main branch — check for unexpected file modifications, new dependencies/network calls, and possible persistence mechanisms (cron jobs, git hooks, CI config changes) before merging.

**Why:** This review step is the final safeguard against a malicious or mistaken agent output; automated agents are powerful but not infallible and should get the same scrutiny as any external contribution.
