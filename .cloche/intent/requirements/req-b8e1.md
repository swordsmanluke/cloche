---
id: req-b8e1
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - parsing saga output
    - saga poll returns markdown
    - batch id not found after saga ask
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: 8mfg-main
    extracted_at: 2026-11-11T21:04:22.815792323Z
    extracted_by: intent-scan
created: 2026-11-11T21:04:22.868681362Z
updated: 2026-11-11T21:04:22.868681362Z
---

When a workflow shells out to the real `saga` CLI, pass `--json` wherever output is parsed and read the batch id and status from the JSON, never by scraping markdown text; `status` is an object with a `state` field.

**Why:** The saga-judge module was first written against a guessed CLI and only passed because its stub was wrong.
