---
id: req-fbb9
status: active
scope:
    level: domain
    domains:
        - web-dashboard
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 9c6473425b4ceb5f8abe4bf7a85f601a6cadb1fd
    extracted_at: 2026-09-16T15:47:41Z
    extracted_by: intent-scan
created: 2026-09-16T15:49:06.908032851Z
updated: 2026-09-16T15:49:06.908032851Z
---

Ledger prompt-revision attribution is recorded at step-dispatch time (keyed by the resolved prompt file and the git commit that last touched it as of dispatch), not recomputed per ledger request. Attempts that predate this recording are backfilled once by a bounded-parallelism background job at daemon startup (`sqlite.Store.RunLedgerPromptRevisionBackfill`), gated via `_migrations` so it resumes across restarts instead of redoing already-finished projects, rather than on the request path. While a project's backfill hasn't completed, `GET /api/projects/{name}/ledger` reports whatever is recorded so far plus `backfill_pending: true` instead of blocking. Per-file `git log --follow` history is cached keyed on the repo's current HEAD (`promptrev.HistoryCache`), so a request only ever shells out again after a new commit.

**Why:** Computing a prompt file's git revision and full git log --follow history per attempt on every ledger request meant one-to-many git subprocess calls per request, making the endpoint prohibitively slow at scale; recording at dispatch time, backfilling historical attempts via a resumable background job, and caching history keeps it fast without blocking reads.
