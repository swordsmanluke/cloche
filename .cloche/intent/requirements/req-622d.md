---
id: req-622d
status: active
scope:
    level: domain
    domains:
        - security-safety
hints:
    - task description comes from an external issue tracker
    - prompt injection risk from user-submitted task text
    - should I trust the task description text as-is in a prompt
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#prompt-hygiene
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.45521235Z
updated: 2026-09-14T15:33:28.45521235Z
---

Sanitize or filter untrusted task input (issue bodies, external task descriptions) in host-side script steps before it reaches an agent prompt, and keep trusted prompt templates explicit — Cloche's prompt assembly concatenates the trusted template with the untrusted user request verbatim.

**Why:** The content of prompts is the primary vector for prompt-injection attacks; separating trusted templates (written and reviewed by the user) from untrusted external content, and sanitizing the latter, reduces the chance of injected instructions overriding the template.
