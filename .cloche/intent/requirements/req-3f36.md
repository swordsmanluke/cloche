---
id: req-3f36
status: superseded
superseded_by: req-fbb9
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - the ledger endpoint is slow or times out
    - how do we know which prompt revision an attempt actually ran with
    - adding a field that needs the prompt file's git history
    - calling git log inside a request handler
    - backfilling historical data for attempts recorded before a new field existed
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/web-dashboard.md#Ledger
    extracted_at: 2026-09-16T15:35:27Z
    extracted_by: intent-scan
created: 2026-09-16T15:36:26.881086325Z
updated: 2026-09-16T15:49:06.908012212Z
---

Ledger prompt-revision attribution is recorded at step-dispatch time (keyed by the resolved prompt file and the git commit that last touched it as of dispatch), not recomputed per ledger request. Pre-existing attempts are backfilled best-effort from the workflow's current prompt-file references and git history at the attempt's start time, and per-file git history is cached rather than shelled out to on every request.

**Why:** Computing a prompt file's git revision and full `git log --follow` history per attempt on every ledger request meant one-to-many git subprocess calls per request, making the endpoint prohibitively slow at scale; recording at dispatch time and caching history keeps it fast.
