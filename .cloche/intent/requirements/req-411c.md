---
id: req-411c
status: superseded
superseded_by: req-6cfa
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - why did intent scan only find the most recent feature
    - multi-repo project scan missing older history
    - intent scan wrapper project repos subdirectory not mined
    - requirements only cover the latest landed work, not the whole project
    - does collect-sources look inside repos/<name>
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: 'docs/intent.md#Extraction: cloche intent scan'
    extracted_at: 2026-09-16T16:40:00Z
    extracted_by: intent-scan
created: 2026-09-16T16:40:44.718672715Z
updated: 2026-09-16T19:12:00.068971313Z
---

collect-sources (the intent-scan mining step) roots every source strictly at the project directory itself — doc globs, git log, and .cloche/runs//.cloche/logs — and never reads config.toml's [[repositories]] entries or descends into wrapped repositories. In a multi-repo wrapper project (real code checked out under e.g. repos/<name>/, declared via [[repositories]]), a scan — first or incremental — only ever mines the wrapper's own tiny git history, docs, and run transcripts, never the wrapped repos' real history, docs, or design decisions.

**Why:** The wrapper's own .git history is typically just one commit per completed task, so this shows up as 'the scan only found the feature that just landed' no matter how long the wrapped repositories' real history is. It's a known, documented gap with a tracked follow-up (making collect-sources iterate every configured repository) — not yet implemented. Until then, keep durable docs at the wrapper root or run `cloche intent scan` from inside the repository you actually want mined.
