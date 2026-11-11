---
id: req-8ce6
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - title starting with a dash
    - user-supplied text passed to bd or gh
    - argument injection in shell script
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: 34qy-main
    extracted_at: 2026-11-11T17:52:17.679367653Z
    extracted_by: intent-scan
created: 2026-11-11T17:52:17.745048122Z
updated: 2026-11-11T17:52:17.745048122Z
---

Module scripts under .cloche/modules/*/scripts that call bd or gh must put '--' before positional arguments (or use the flag form, e.g. `bd create --title`) so a title, comment or id starting with '-' is not parsed as a flag.

**Why:** A title of '--help' was read as a flag by the tracker CLI; tests should exercise such a value.
