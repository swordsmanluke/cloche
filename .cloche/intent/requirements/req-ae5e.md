---
id: req-ae5e
status: active
scope:
    level: domain
    domains:
        - container-runtime
hints:
    - clean snapshot creation failed for a run
    - what happens if the git clone for container seeding errors
    - container seeding fallback behavior on snapshot failure
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/run-isolation/architecture.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454122251Z
updated: 2026-09-14T15:33:28.454122251Z
---

If the clean per-run snapshot can't be materialized (non-git project directory, empty base SHA, or a declared child repository that fails to clone), the daemon falls back to seeding the container from the live working tree instead of failing the run.

**Why:** The clean-snapshot protection should never turn into a hard outage; a degraded seed (with the known live-tree risk) is preferable to blocking the run entirely.
