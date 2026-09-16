---
id: req-6cfa
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - intent scan only found the feature that just landed
    - multi-repo project requirements are missing history
    - does intent scan look inside repos/<name>
    - requirements only cover the most recent commit on a wrapper project
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#Multi-repo projects
    extracted_at: 2026-09-16T12:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:12:00.069240943Z
updated: 2026-09-16T19:12:00.069240943Z
---

intent-scan's collect-sources step mines both the project wrapper's own docs/git log/run transcripts AND, for each [[repositories]] entry in config.toml, that repository's own docs, git log, and .cloche/runs (if it has its own .cloche/ state) — each source keeps its own independent scan cursor in scan-state.yaml.

**Why:** In a thin orchestration-wrapper layout, the wrapper's own .git history is typically tiny (often one commit per completed task) while the real project history and design decisions live inside the wrapped repository's own .git and docs; scanning only the wrapper root would make requirements cover only whatever feature just landed, not the life of the project.
