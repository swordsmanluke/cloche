---
id: req-bea6
status: active
scope:
    level: domain
    domains:
        - container-runtime
        - dras
hints:
    - leftover cloche-dra egress containers
    - container kept with --keep-container still has proxy running
    - docker network cannot be removed while attached
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: bvb9-main
    extracted_at: 2026-11-11T21:07:19.865683258Z
    extracted_by: intent-scan
created: 2026-11-11T21:07:19.967474738Z
updated: 2026-11-11T21:07:19.967474738Z
---

A locked container's egress sidecar must be stopped and removed when the run ends, even if the main container is kept; the network is removed only with the main container, and teardownEgress must be safe to call twice.

**Why:** Kept or failed DRA runs otherwise leave proxies running that still forward to the daemon and Saga ports.
