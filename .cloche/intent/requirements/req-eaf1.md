---
id: req-eaf1
status: active
scope:
    level: domain
    domains:
        - agent-adapters
hints:
    - agent_args isn't disabling the stream-json output
    - can I remove --verbose from the claude invocation
    - why does cloche always add --format json for opencode
    - custom agent_args and the CLOCHE_RESULT marker not being read
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/built-in-agents.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.456458444Z
updated: 2026-09-14T15:33:28.456458444Z
---

Known agents' required arguments (e.g. `claude`'s `--output-format stream-json --verbose`, `opencode`'s `--format json`) are always injected regardless of what `agent_args` specifies, and cannot be removed.

**Why:** The prompt adapter parses the agent's structured/streaming output to extract the `CLOCHE_RESULT` marker and token usage; without these flags the adapter has no way to read a result back from the agent process.
