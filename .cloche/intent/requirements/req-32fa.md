---
id: req-32fa
status: active
scope:
    level: domain
    domains:
        - web-dashboard
        - security-safety
        - dras
hints:
    - how is the signals endpoint authenticated
    - can a DRA call dashboard APIs
    - which token does /mcp accept
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 80d176c320f3c67a0d59f2b436201676ded90beb
    extracted_at: 2026-11-11T19:53:14.490437814Z
    extracted_by: intent-scan
created: 2026-11-11T19:53:14.541496898Z
updated: 2026-11-11T19:53:14.541496898Z
---

Credentials that remain after the dashboard token removal: the per-project signal token (signals and tasks/create|update), the per-run /mcp token, and the per-wake-up DRA credential; each is separate and none accepts another's token.

**Why:** Routes that carry their own credential stay guarded even though the rest of the dashboard is open; the DRA limits currently hold only for a DRA that uses the CLI it is given, since the dashboard and gRPC port are unauthenticated.
