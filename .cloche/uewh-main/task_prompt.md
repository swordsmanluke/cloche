## Task: Status: Show human step details in `cloche status` / `cloche list`

The `RunStateWaiting` state and color support were implemented (cloche-q1kn), but the design requires surfacing additional detail for runs waiting at a human step: the step name, time since last poll, and poll count. This data exists in `HumanPollRecord` in the DB but is never fetched or displayed.

## What's missing

- `cloche status <task-id>` should show: step name being polled, elapsed time since last poll, poll count
- `cloche list` should distinguish waiting runs from running ones with at minimum the step name

## Implementation

1. Add a gRPC endpoint (or extend an existing one) to fetch `HumanPollRecord` for a given run ID — `ListHumanPolls` already exists on the store
2. Update `cmdStatus()` and `cmdList()` in `cmd/cloche/main.go` to call the endpoint and display the details when a run is in `waiting` state
3. Format: e.g. `waiting at "code-review" — last polled 4m ago (3 polls)`

## Files to change

- `internal/adapters/grpc/server.go` — new or extended endpoint
- `internal/protocol/cloche.proto` (if proto changes needed)
- `cmd/cloche/main.go` — `cmdStatus()` and `cmdList()`

Ref: docs/design/human-in-the-loop.md — Orchestration and State section
