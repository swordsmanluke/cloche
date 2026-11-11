---
id: req-01e4
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - workflow-dsl
hints:
    - script step picked the wrong result
    - CLOCHE_RESULT in echoed output
    - result marker spoofing
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md
    extracted_at: 2026-11-11T18:09:17.043902799Z
    extracted_by: intent-scan
created: 2026-11-11T18:09:17.145005909Z
updated: 2026-11-11T18:09:17.145005909Z
---

Host script steps that shell out to other commands should frame their result marker as CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:<name> so unrelated CLOCHE_RESULT text in their output is ignored.

**Why:** The executor prefers a nonce-framed marker and falls back to the bare form; collected docs or commit messages may contain marker-like text.
