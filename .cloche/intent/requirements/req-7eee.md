---
id: req-7eee
status: active
scope:
    level: domain
    domains:
        - security-safety
        - container-runtime
hints:
    - should cloche commit notes.md
    - what can a DRA container do
    - why can't the DRA run go build
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: odk7-main
    extracted_at: 2026-11-11T19:21:22.374725056Z
    extracted_by: intent-scan
created: 2026-11-11T19:21:22.425997778Z
updated: 2026-11-11T19:21:22.425997778Z
---

A DRA is a directory .cloche/dras/<name>/ with a required charter.md and notes.md; cloche never commits notes.md, leaving git handling to the project. DRA runs use a locked-down image: no compilers, no push credentials, no tracker CLI or data, read-only project except the DRA's own notes.md, and a narrow network allowlist.

**Why:** Limits the blast radius of a long-lived autonomous agent (rulings CLOCHE-A117, A118, A120, A123, A128, A130).
