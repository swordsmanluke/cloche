---
id: req-e1b4
status: active
scope:
    level: domain
    domains:
        - cli
hints:
    - CLI flag validation happening before or after dialing the daemon
    - should invalid flags fail fast without a daemon connection
confidence: low
user_edited: false
provenance:
    kind: prompt
    ref: 1vc6-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462584646Z
updated: 2026-09-15T00:41:34.462584646Z
---

CLI commands should validate incompatible flag combinations locally and error out before dialing the daemon (e.g. --no-git requires --at), rather than making an RPC and failing server-side.

**Why:** Shown as the pattern for cloche extract, with an explicit fail-fast, no-dial test.
