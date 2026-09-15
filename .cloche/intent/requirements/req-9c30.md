---
id: req-9c30
status: active
scope:
    level: domain
    domains:
        - agent-adapters
hints:
    - writing a new prompt template step
    - agent step keeps failing with no marker
    - what should a prompt file's ending look like
    - every run fails right after a cloche-agent rebuild
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: a1e98a2
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462107293Z
updated: 2026-09-15T00:41:34.462107293Z
---

Every prompt template should include an explicit result-reporting section spelling out the literal CLOCHE_RESULT:{{ $result_nonce }}:<name> marker syntax, rather than relying on inferred success language.

**Why:** A marker-contract tightening once broke every container run at once because 13 prompt files lacked this section and had been relying on lenient inference; the mismatch stayed invisible until the stricter agent binary shipped.
