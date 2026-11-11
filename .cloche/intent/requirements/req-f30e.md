---
id: req-f30e
status: active
scope:
    level: domain
    domains:
        - signals
        - grpc-protocol
hints:
    - 401 on tasks create
    - signal flood
    - comma in signal_source
    - 429 too many signal handlers
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: ae4503b9d2a521ec38296434d8b53c8386d6169c
    extracted_at: 2026-11-11T17:52:17.679367653Z
    extracted_by: intent-scan
created: 2026-11-11T17:52:17.745697219Z
updated: 2026-11-11T17:52:17.745697219Z
---

The tasks create/update HTTP endpoints require the project's signal token as a bearer token, and signal_source must be 1-128 characters of letters, digits, '.', '_' or '-' (rejected with 400 otherwise); signal handlers are limited to 8 concurrent and 30 starts per hour per source (429).

**Why:** Outside input to signal intake must be authenticated, validated (a comma in signal_source added extra labels) and rate limited.
