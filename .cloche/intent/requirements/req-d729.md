---
id: req-d729
status: active
scope:
    level: domain
    domains:
        - security-safety
hints:
    - should this agent step run in a host workflow or a container
    - is it safe to put a prompt step directly in a host block
    - host workflow agent step security risk
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#minimize-host-side-agent-usage
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454672421Z
updated: 2026-09-14T15:33:28.454672421Z
---

Prefer dispatching agent work to container workflows over running agent steps directly in a host workflow. Host-side agent steps inherit the daemon's full OS privileges (filesystem, network, environment) with no isolation.

**Why:** Container workflows are the isolation boundary Cloche relies on; an agent step running in a `host {}` block has none of that protection, so it should be reserved for orchestration (scripts and workflow dispatch), not direct LLM work.
