---
id: req-4a9d
status: active
scope:
    level: domain
    domains:
        - container-runtime
hints:
    - cloche console container isn't showing up in cloche list
    - leftover containers from cloche console piling up
    - how to clean up a console session
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#cloche-console
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462488675Z
updated: 2026-09-15T00:41:34.462488675Z
---

cloche console starts an interactive session using the same environment as a workflow run, but its container is never auto-removed and never appears in cloche list — clean it up manually with cloche delete <container-id> or docker rm.

**Why:** Distinguishes console containers from workflow-run containers, which are tracked and auto-cleaned on success.
