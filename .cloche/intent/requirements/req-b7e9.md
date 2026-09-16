---
id: req-b7e9
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - host workflow run failed but error_message is empty
    - why does cloche list --runs show failed with no reason
    - console needs-you item has no explanation for a failure
    - debugging a silent host workflow failure
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 63104e0db7631a322da2e79b2da89fb2f648c13a
    extracted_at: 2026-09-16T20:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:58:52.347353847Z
updated: 2026-09-16T19:58:52.347353847Z
---

When a host workflow step reaches `abort` via a declared `fail` wire (not a Go-level engine error), the daemon records a human-readable ErrorMessage on the host Run naming the failing step and its result, rather than leaving the run's error message empty.

**Why:** Runs that failed this way previously showed up in `cloche list --runs` and the console's Needs You / builtin-failures item as bare failures with no explanation, so diagnosing automated failures (e.g. the intent-scan reconcile bug) required reading raw run logs instead of the failure surface.
