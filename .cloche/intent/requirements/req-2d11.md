---
id: req-2d11
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - retry branch has leftover commits from the failed attempt
    - why does a retry reset the branch instead of continuing it
    - attempt branch naming and base
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: e3bn-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461739467Z
updated: 2026-09-15T00:41:34.461739467Z
---

Retries of a failed task always start a fresh branch from the original base SHA, never from the failed attempt's branch tip.

**Why:** Keeps a retry isolated from a possibly-corrupted prior attempt rather than building on top of it.
