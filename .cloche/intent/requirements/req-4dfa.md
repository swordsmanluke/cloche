---
id: req-4dfa
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - core-domain-ports
hints:
    - step failed with contract error
    - workflow cannot find task_id in kv
    - remap a kv key for a child workflow
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-10-13-modules-design.md#Contracts
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.453945369Z
updated: 2026-10-15T15:54:45.453945369Z
---

Contracts are enforced at runtime: a missing `kv:` input or `env:` var fails the calling step before the child run starts, and a workflow ending in done must have set every declared `out:` key or the step fails. A `with` block applies only to the step it appears in; undeclared extra keys may still be written.

**Why:** Implicit KV data flow between workflows made failures hard to trace; declared inputs/outputs make them fail early with a clear message.
