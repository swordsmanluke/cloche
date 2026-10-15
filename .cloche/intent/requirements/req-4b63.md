---
id: req-4b63
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - requirements not being injected into prompts
    - domains.yaml parsing error
    - intent silently off after scan
    - yaml mapping values are not allowed
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: r2nv-main
    extracted_at: 2026-10-15T15:57:31.706145393Z
    extracted_by: intent-scan
created: 2026-10-15T15:57:31.753983758Z
updated: 2026-10-15T15:57:31.753983758Z
---

Intent-scan's agent-written domains.yaml must be valid YAML; a malformed file makes intent.Resolve fail on every agent step so no requirements are injected, while the scan itself keeps succeeding.

**Why:** Descriptions containing ': ' emitted unquoted broke parsing ("mapping values are not allowed"); in the A/B experiment 10/12 tasks ran uninjected without any visible failure. Failure is silent and the daemon log line does not name the project.
