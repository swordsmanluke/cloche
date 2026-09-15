---
id: req-32fb
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - why does doc-report.md look like an open backlog
    - committed report shows findings that were already fixed
    - is this documentation finding still open or already resolved
    - document workflow report format
    - doc-report.md is stale
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: runs/3lcz-main/task_prompt.md
    extracted_at: 2026-09-15T18:01:18Z
    extracted_by: intent-scan
created: 2026-09-15T18:02:25.907547106Z
updated: 2026-09-15T18:02:25.907547106Z
---

The document workflow's report file (doc-report.md) must be updated by write-docs to record each finding's outcome — resolved (with the commit-time state) or not-resolved with a reason — instead of staying frozen at the pre-fix state written by the investigate step.

**Why:** The committed report was a snapshot of problems as originally found, not as resolved. Most findings were fixed by write-docs in the same run that produced the report, but the artifact never recorded it, so it permanently read like a live backlog of open documentation defects and every entry had to be re-derived from source to discover it was stale.
