---
id: req-824e
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - step failed for no reason after tests passed
    - working on the CLOCHE_RESULT classifier itself
    - marker false-positive from grep or test-fixture output
    - editing marker/protocol test fixtures in cloche's own repo
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: nm1p-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462204597Z
updated: 2026-09-15T00:41:34.462204597Z
---

The CLOCHE_RESULT marker protocol is in-band and unescaped, so when working on cloche's own marker/classifier code, avoid letting grep output or test fixtures echo literal CLOCHE_RESULT: strings — a stray quoted marker in your own step's transcript can poison that step's own classification.

**Why:** Observed incident: an agent correctly fixed and tested the classifier, but its own grep/test-fixture output contained dozens of stray CLOCHE_RESULT: strings, so the classifier picked one up and discarded the passing fix as a failed run.
