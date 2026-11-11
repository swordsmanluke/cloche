---
id: req-7b6d
status: active
scope:
    level: domain
    domains:
        - dras
hints:
    - dra directory name rules
    - what makes a valid dra
    - missing charter.md
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/dras.md#layout
    extracted_at: 2026-11-11T19:27:03.94283836Z
    extracted_by: intent-scan
created: 2026-11-11T19:27:03.994446802Z
updated: 2026-11-11T19:27:03.994446802Z
---

A DRA is defined by .cloche/dras/<name>/ with a required charter.md; the directory name follows the label rule (lowercase letters, digits, '-'). Zero DRAs is the default and changes nothing.

**Why:** `cloche validate` reports a DRA with no charter.md or an invalid name; existing projects must be unaffected.
