---
id: req-6c3c
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - reviewer says the change is wrong
    - should the fix agent delete the failing test
    - who reviews agent code
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: 8wel-main
    extracted_at: 2026-11-11T01:36:38.712348365Z
    extracted_by: intent-scan
created: 2026-11-11T01:36:38.771112312Z
updated: 2026-11-11T01:36:38.771112312Z
---

The review step in develop.cloche uses a different agent session than the one that wrote the change (the default agent in a fresh session); which agent reviews is a per-step DSL choice. It reports fail only with evidence: a failing test or a named breaking input, and writes findings where the fix prompt reads them.

**Why:** An agent that didn't write the change tries to break it; unevidenced review failures would churn the fix loop. Fix must keep the reviewer's failing tests and not weaken them.
