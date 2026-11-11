---
id: req-a3a6
status: active
scope:
    level: domain
    domains:
        - container-runtime
hints:
    - agent started in a container with writable workspace
    - shell script continues after a failed chown
    - wrapper command joined with semicolon
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: uvet-main
    extracted_at: 2026-11-11T21:04:22.815792323Z
    extracted_by: intent-scan
created: 2026-11-11T21:04:22.868961863Z
updated: 2026-11-11T21:04:22.868961863Z
---

A locked-down container must fail closed: if any root-side lock-down setup step fails, the container exits non-zero without starting the agent and the run fails with an error saying the lock-down could not be applied. Only the chown of /home/agent/.claude may fail silently.

**Why:** Long-lived agents run in locked-down containers (CLOCHE-A118); a `; exec` after failed setup would start the agent with writable project files.
