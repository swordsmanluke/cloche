---
id: req-7f22
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - local model returning empty completions
    - bonsai wrapper returns 0 chars
    - single-shot completion agent can't read DESIGN.md
    - weak model harness needs spec inlined
    - why is the wrapper's output empty
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 54b414bbe6e7baa78e0158384bd36e45a4611daa
    extracted_at: 2026-09-16T15:18:21Z
    extracted_by: intent-scan
created: 2026-09-16T15:20:04.829615784Z
updated: 2026-09-16T15:20:04.829615784Z
---

The intent-ab experiment's local-model wrapper (a single-shot completion, not a tool-using agent) must inline the project's DESIGN.md into the prompt it builds, since it has no way to read files itself.

**Why:** Task prompts routinely tell the model to "read DESIGN.md" before writing code, but a single-shot completion has no tool to do that. Verified against a real pilot task prompt: 3/3 raw completions came back 0 chars without the spec inlined, versus a substantive attempt once it was.
