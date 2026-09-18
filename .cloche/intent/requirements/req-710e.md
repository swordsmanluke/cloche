---
id: req-710e
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - task not showing under the right repo sub-tab
    - multi-repo run attribution wrong in the dashboard
    - how does the console decide which repo a task belongs to
    - ResolveRunRepositories
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/CHANGELOG-DETAILED.md#v3.24.14
    extracted_at: 2026-09-18T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-18T21:31:56.554987042Z
updated: 2026-09-18T21:31:56.554987042Z
---

A task's web-console repo sub-tab attribution is the union of: the host workflow's own declared repo(s), a step's `repository` config pin, the repos a container sub-workflow actually extracted results into, and (for `cloche run` invoked from inside a `[[repositories]]` sub-repo path) the matched repo — not just the workflow's single-repo `repos` declaration. A task matching none of these is counted separately as "unattributed" under all-repos rather than silently missing from a named sub-tab.

**Why:** A host workflow declaring no `repos` at all defaults to touching every configured repository, so attributing it only from its own declaration previously left multi-repo or no-repo-declared runs stuck under "all repos" only (fixed in 537715c).
