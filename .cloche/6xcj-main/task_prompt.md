## Task: Loop: Poll human steps in orchestration loop

Extend the orchestration loop in `internal/host/loop.go` to drive human step polling.

- On each loop tick, scan active workflow runs to find any currently sitting at a `human` step
- For each such run, check the `last_run` timestamp from the DB: if `now >= last_run + interval`, invoke the polling script via the executor and update `last_run`
- The loop tick frequency must be high enough that human step polls fire within ~30 seconds of their idealized trigger time
- The `interval` is a 'no sooner than' constraint — exact timing is not guaranteed
- Skip the poll if an invocation for that `(run_id, step_name)` is already in-flight (handled by executor)

Ref: docs/design/human-in-the-loop.md — Orchestration and State section

---

## REJECTION: Previous implementation is wrong — do it right

The previous attempt implemented polling as a self-contained blocking loop inside `executeHuman()` in `internal/host/executor.go`. This is NOT acceptable.

The design explicitly requires the orchestration loop (`internal/host/loop.go`) to drive polling on each tick — not a blocking goroutine inside the executor. This design decision exists for a specific reason: daemon restart recovery. When the daemon restarts, the loop naturally re-discovers all runs sitting at a human step and resumes polling on the next tick. A blocking goroutine in the executor is killed on restart and cannot recover without manual intervention.

Requirements:
1. Human steps must NOT block in the executor. The executor should dispatch a single poll invocation and return `pending`. The loop is responsible for re-invoking.
2. `internal/host/loop.go` must be modified to scan for runs at a human step on each tick and invoke the next poll when `now >= last_run + interval`.
3. The `HumanPollStore` (already implemented in cloche-q3ka) tracks `last_run` per `(run_id, step_name)` — use it.
4. On daemon restart, the loop discovers pending human steps automatically on its first tick. No special recovery code needed.

Read docs/design/human-in-the-loop.md in full before implementing.
