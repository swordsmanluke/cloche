---
id: req-3feb
status: active
scope:
    level: domain
    domains:
        - persistence
        - signals
hints:
    - signals table growing
    - database size
    - retention of old signals
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: 34qy-main
    extracted_at: 2026-11-11T17:52:17.679367653Z
    extracted_by: intent-scan
created: 2026-11-11T17:52:17.745950718Z
updated: 2026-11-11T17:52:17.745950718Z
---

The signals table must be pruned: rows older than 30 days are deleted at daemon startup.

**Why:** Each row can hold 64 KB of evidence and the table otherwise only grows.
