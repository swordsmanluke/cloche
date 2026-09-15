---
id: req-97d8
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - clo get returns nothing for my config key
    - step config key missing from KV
    - picking a name for a new step config key
    - engine-reserved config keys
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: 7a9f-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461821182Z
updated: 2026-09-15T00:41:34.461821182Z
---

A fixed set of step Config keys (prompt, run, workflow_name, agent_command, agent_args, agent, results, feedback, prompt_step, usage_command, max_attempts, timeout) is consumed internally by the engine and must never be written to by step-authored config.

**Why:** Prevents step-authored config from clashing with engine-internal wiring keys.
