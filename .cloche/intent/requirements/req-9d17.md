---
id: req-9d17
status: active
scope:
    level: domain
    domains:
        - container-runtime
        - security-safety
hints:
    - why are auth files copied instead of mounted into the container
    - container credential isolation
    - does a container run share Claude Code session state with the host
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#Container Isolation Model
    extracted_at: 2026-09-16T12:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:12:00.069139281Z
updated: 2026-09-16T19:12:00.069139281Z
---

The three Claude Code auth files copied from ~/.claude/ into each container are copied, not bind-mounted, so every container gets its own isolated copy.

**Why:** Bind-mounting the host's credential files would let a container write back to (or otherwise affect) the host's auth state; a copy keeps each container's credential access isolated per run.
