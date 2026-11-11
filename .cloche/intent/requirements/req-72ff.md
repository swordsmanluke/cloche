---
id: req-72ff
status: active
scope:
    level: domain
    domains:
        - dras
        - grpc-protocol
        - security-safety
hints:
    - is gRPC port authenticated
    - can a DRA call shutdown over gRPC
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/docs/dras.md#Daemon access (the wake-up credential)
    extracted_at: 2026-11-11T19:36:14.872604854Z
    extracted_by: intent-scan
created: 2026-11-11T19:36:14.930404017Z
updated: 2026-11-11T19:36:14.930404017Z
---

A DRA's daemon access over gRPC is not yet authenticated (the gRPC port is unauthenticated for clo and cloche-agent); the DRA restrictions only cover the HTTP API and CLI, so don't assume stop/delete/shutdown are blocked over gRPC.

**Why:** Known gap documented pending gRPC authentication or network allowlist work.
