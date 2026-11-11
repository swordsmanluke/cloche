---
id: req-18ec
status: active
scope:
    level: domain
    domains:
        - project-config
        - container-runtime
hints:
    - append to a list in merge override
    - delete a key with merge file
    - merge target is clocheignored
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-11-07-override-merge-design.md
    extracted_at: 2026-11-11T00:16:38.456159878Z
    extracted_by: intent-scan
created: 2026-11-11T00:16:38.512687195Z
updated: 2026-11-11T00:16:38.512687195Z
---

Merge overrides follow JSON Merge Patch (RFC 7386): maps merge recursively, scalars and lists replace (lists are never appended), null deletes a key. TOML has no null so TOML merges cannot delete keys. Merging happens on the host against the file as copied into the container, never reading host files the container was not meant to see (.clocheignore'd or outside selected repos).

**Why:** Keeps merge predictable and respects the container's view of the project.
