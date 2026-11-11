---
id: req-22c7
status: active
scope:
    level: domain
    domains:
        - usage-budget
hints:
    - how is usage measured
    - estimating token cost of a run
    - budget baseline after daemon restart
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#[budget]
    extracted_at: 2026-11-11T00:33:44.93352215Z
    extracted_by: intent-scan
created: 2026-11-11T00:33:44.981584734Z
updated: 2026-11-11T00:33:44.981584734Z
---

Budget usage is measured by reading the account's weekly usage percentage via `claude -p \"/usage\"` (no model call, no tokens); used-today is the difference from a baseline taken at the start of the local day, persisted in ~/.config/cloche/budget.json. Do not add token-estimate settings.

**Why:** The allowance is directly readable, so weekly-size or per-model-weight estimates are unnecessary; interactive Claude sessions count too. Only the Claude allowance is covered.
