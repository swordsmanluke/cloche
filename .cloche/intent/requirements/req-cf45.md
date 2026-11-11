---
id: req-cf45
status: active
scope:
    level: domain
    domains:
        - security-safety
        - signals
        - web-dashboard
hints:
    - 401 on signals endpoint
    - rotate signal token
    - expose dashboard on the network
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#signal-intake-token
    extracted_at: 2026-11-11T01:49:47.526697151Z
    extracted_by: intent-scan
created: 2026-11-11T01:49:47.590313327Z
updated: 2026-11-11T01:49:47.590313327Z
---

The signal intake endpoint POST /api/projects/{name}/signals requires a per-project bearer token (0600 under ~/.config/cloche/signal-tokens/); never store it in repos, Dockerfiles or overrides, and do not expose the dashboard listener to untrusted networks without TLS.

**Why:** A signal can create a task which spends budget and steers an agent; the rest of the dashboard HTTP listener is unauthenticated.
