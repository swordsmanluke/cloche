---
id: req-409b
status: active
scope:
    level: domain
    domains:
        - container-runtime
        - daemon-host-orchestration
hints:
    - extracted commit has cloche run title
    - Invalid revision range in container
    - agent commit message missing on feature branch
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: b7f6360e38b72319bc90617d208581e5050f30e5
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.454768414Z
updated: 2026-10-15T15:54:45.454768414Z
---

Extraction must resolve the base SHA recorded when the extract worktree was prepared, never re-read the repo's HEAD at extraction time, because HEAD moves while the container runs (e.g. an intent scan commits scan state).

**Why:** A re-resolved base SHA made `git log <base>..HEAD` fail inside the container, dropping agent commit messages so the commit fell back to the generic "cloche run" title.
