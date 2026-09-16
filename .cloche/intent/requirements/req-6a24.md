---
id: req-6a24
status: active
scope:
    level: domain
    domains:
        - persistence
hints:
    - new sqlite migration dropped an index
    - where to add a new migration in store.go migrate()
    - runs table indexes disappearing after migration
    - migration ordering in sqlite store
    - query plan shows SCAN instead of SEARCH after a migration
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 01502b2cdf849901ff8b4d045ad0614eb1cdbff5
    extracted_at: 2026-09-16T15:18:21Z
    extracted_by: intent-scan
created: 2026-09-16T15:20:04.830090081Z
updated: 2026-09-16T15:20:04.830090081Z
---

In internal/adapters/sqlite/store.go's migrate(), the secondary-indexes migration (migrateSecondaryIndexes) must run after migrateRunsCompositeKey, because that migration recreates the runs table and would otherwise drop any indexes created before it.

**Why:** runs, step_executions, attempts and log_files previously had only primary keys, making common lookups (by project_dir, task_id, parent_run_id, attempt_id, state, run_id) full table scans; the fix adds a migrations-table-gated index migration, but ordering relative to the table-recreating composite-key migration matters for the indexes to actually stick.
