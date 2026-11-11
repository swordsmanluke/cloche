---
id: req-998a
status: active
scope:
    level: domain
    domains:
        - dras
hints:
    - braces in prompt text
    - template injection in wake message
    - passing event text to a prompt
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/dras.md#Wake-ups
    extracted_at: 2026-11-11T19:44:06.339123008Z
    extracted_by: intent-scan
created: 2026-11-11T19:44:06.388970389Z
updated: 2026-11-11T19:44:06.388970389Z
---

DRA wake-up messages are written to a file (wake-message.md) rather than interpolated into the prompt.

**Why:** A message containing `{{` must never be read as a prompt template.
