---
id: req-7ded
status: active
scope:
    level: domain
    domains:
        - cli
hints:
    - tab completion is missing my local workflow names
    - completion shows the wrong workflows when CLOCHE_ADDR points elsewhere
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 54f4348
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462300839Z
updated: 2026-09-15T00:41:34.462300839Z
---

Shell-completion candidates from the local project's .cloche/ (static) must take precedence over, and be merged with rather than shadowed by, the daemon's dynamic completions, since a daemon reached via CLOCHE_ADDR may be answering for a different filesystem view of the project.

**Why:** Fixed after the daemon's answer was fully replacing the static one, e.g. hiding local workflow names when CLOCHE_ADDR pointed at a daemon serving a container's view of the project.
