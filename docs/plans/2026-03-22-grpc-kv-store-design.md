# gRPC-Backed Key-Value Store for Workflow Steps

**Date:** 2026-03-22
**Status:** Design

## Problem

Workflow steps pass data to each other via a JSON file at
`.cloche/runs/<taskID>/context.json`. This works on the host but creates
friction in containers: files have to be pre-seeded before the container
starts, values set inside a container aren't visible to host steps, and the
whole mechanism depends on copying files into ignored directories. There is no
way for a container step to pass structured data back to a host workflow step
without extracting files from the container after it exits.

## Solution

Move the key-value store behind the daemon's gRPC API. The daemon holds
per-attempt KV namespaces in SQLite. All participants — host steps, container
steps, the CLI — read and write through the same RPCs. A new lightweight
in-container CLI tool (`clo`) gives container code access to the store without
needing the full `cloche` client.

This eliminates file-copying mechanics, makes cross-boundary data passing
trivial, and lets host workflows wrap dangerous prompt-steps inside isolated
container runs: pass arguments in, extract results out, all through a
controlled API.

## Design Details

### gRPC API

Two new RPCs on `ClocheService`:

```protobuf
rpc GetContextKey(GetContextKeyRequest) returns (GetContextKeyResponse);
rpc SetContextKey(SetContextKeyRequest) returns (SetContextKeyResponse);

message GetContextKeyRequest {
  string task_id    = 1;
  string attempt_id = 2;
  string key        = 3;
}

message GetContextKeyResponse {
  string value = 1;
  bool   found = 2;
}

message SetContextKeyRequest {
  string task_id    = 1;
  string attempt_id = 2;
  string key        = 3;
  string value      = 4;
}

message SetContextKeyResponse {}
```

**Value size limit:** 1 KB per key. The daemon rejects `SetContextKey` with
`INVALID_ARGUMENT` if `len(value) > 1024`.

**Key format:** Freeform strings within an attempt namespace. No restrictions
beyond non-empty.

### Storage

New SQLite table in the daemon's existing database:

```sql
CREATE TABLE context_kv (
  task_id    TEXT NOT NULL,
  attempt_id TEXT NOT NULL,
  key        TEXT NOT NULL,
  value      TEXT NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (task_id, attempt_id, key)
);
```

The `RunStore` port gains two methods:

```go
type RunStore interface {
    // ... existing methods ...
    GetContextKey(ctx context.Context, taskID, attemptID, key string) (string, bool, error)
    SetContextKey(ctx context.Context, taskID, attemptID, key, value string) error
}
```

The SQLite adapter (`internal/adapters/sqlite/`) implements these with simple
`SELECT`/`INSERT OR REPLACE` queries.

### Namespace and Lifecycle

Keys are namespaced by `(taskID, attemptID)`. Each new attempt starts with a
clean slate. When a run is resumed, the daemon creates a new attempt and copies
all KV pairs from the previous attempt into it (same as other resume state).

Cleanup: when a run reaches a terminal state and its context is no longer
needed, the daemon deletes all rows for that `(taskID, attemptID)`. This
mirrors the current `runcontext.Cleanup()` behavior.

### Auto-Seeded Keys

The daemon seeds these keys when an attempt starts:

| Key | Value |
|---|---|
| `task_id` | Task identifier |
| `attempt_id` | Attempt identifier |
| `run_id` | Run identifier |
| `workflow` | Current workflow name |

Before each step executes, the executor (host or agent) updates:

| Key | Value |
|---|---|
| `workflow` | Current workflow name (updated on every workflow transition) |
| `prev_step` | Name of the preceding step, or `""` for the entry step |
| `prev_step_exit` | Result wire name from the preceding step (e.g. `success`, `fail`), or `""` |

An attempt may span multiple workflows (e.g. `main` dispatches `develop`, then
`finalize`). The `workflow` key is updated whenever execution enters a
different workflow, so it always reflects the currently executing one.

These replace the current `runcontext.SeedRunContext()` and
`runcontext.SetPrevStep()` calls.

### `clo` — In-Container CLI

A new binary at `cmd/clo/`. Lightweight gRPC client baked into cloche
container images. Shares the version string with the other binaries.

**Commands:**

```
clo get <key>             Print value to stdout; exit 1 if not found
clo set <key> <value>     Set a key; read value from stdin if value is "-"
clo set <key> -f <file>   Set a key from file contents
clo keys                  List all keys in the current attempt namespace
clo -v / clo --version    Print version
```

**Environment variables** (set by the Docker adapter when creating the
container):

| Variable | Purpose |
|---|---|
| `CLOCHE_ADDR` | Daemon gRPC address (TCP, e.g. `host.docker.internal:50051`) |
| `CLOCHE_TASK_ID` | Already set by Docker adapter |
| `CLOCHE_ATTEMPT_ID` | Already set by Docker adapter |

`clo` reads these three variables, dials the daemon, and calls
`GetContextKey`/`SetContextKey` with the task and attempt IDs.

### Daemon TCP Listener

The daemon already supports TCP via its `listen()` function in
`cmd/cloched/main.go`. To make the store reachable from containers, the daemon
starts an additional TCP listener alongside its Unix socket. The TCP port is
configurable:

- Env: `CLOCHE_TCP` (e.g. `127.0.0.1:50051`)
- Config: `daemon.tcp` in `~/.config/cloche/config`
- Default: `127.0.0.1:50051`

The Docker adapter already passes `--add-host=host.docker.internal:host-gateway`
to containers, so `clo` reaches the daemon at `host.docker.internal:50051`.

Both listeners serve the same gRPC service. The Unix socket remains the default
for host-side CLI communication (lower overhead, no TCP port exposure).

### Docker Adapter Changes

`internal/adapters/docker/runtime.go` passes the daemon address to the
container:

```go
env = append(env, "CLOCHE_ADDR=host.docker.internal:50051")
```

The `clo` binary is already present in the container image (added to the base
`cloche-agent` Dockerfile).

### Host-Side `cloche get/set` Migration

`cmd/cloche/main.go`: `cmdGet()` and `cmdSet()` switch from calling
`runcontext.Get/Set` (local file) to calling `GetContextKey`/`SetContextKey`
via gRPC, same as they do for other daemon RPCs.

The `CLOCHE_TASK_ID` and `CLOCHE_ATTEMPT_ID` env vars are already available in
host workflow steps. `CLOCHE_ADDR` falls back to the Unix socket for host-side
use (same as other cloche commands).

### `runcontext` Package Removal

Once the migration is complete, the file-based `internal/runcontext/` package
is removed. The prompt file (`.cloche/runs/<taskID>/prompt.txt`) moves to a
dedicated mechanism or goes through the KV store if it fits within 1 KB.
Prompts that exceed 1 KB continue to use a file passed via Docker volume.

### Step Result Recording

`runcontext.SetStepResult()` currently writes keys like
`develop:implement:result = success`. This moves to the KV store with the same
key format. The engine and executors call `SetContextKey` instead.

### Error Handling

- **Daemon unreachable:** `clo get/set` and `cloche get/set` print an error
  and exit 1. Workflow steps that depend on KV data fail with a clear message
  rather than silently using stale file data.
- **Key not found:** `GetContextKey` returns `found=false`. CLI exits 1 with
  a message. Scripts can check the exit code.
- **Value too large:** `SetContextKey` rejects with `INVALID_ARGUMENT` and a
  message indicating the 1 KB limit. CLI prints the error and exits 1.
- **Daemon restart:** KV data is in SQLite, so it survives daemon restarts.
  In-flight containers reconnect on next `clo` call (each call is a short-lived
  gRPC connection).

## Alternatives Considered

**Mount Unix socket into container.** Simpler than TCP, but ties us to POSIX
and complicates Windows/non-Linux support. TCP is a small addition and the
daemon already supports it.

**Extend `cloche-agent` instead of a new `clo` binary.** `cloche-agent` is
the autonomous workflow runner — it parses DSL, walks the graph, streams
status. Overloading it as a general-purpose CLI tool conflates two roles. A
separate `clo` binary is small (thin gRPC client), single-purpose, and its
name clearly signals "lightweight in-container tool."

**Keep file-based store, sync via Docker volumes.** This is essentially the
status quo with extra plumbing. It doesn't solve the cross-boundary problem
and adds complexity around file synchronization timing.

**Pass data through the status stream protocol.** The agent already streams
status to the daemon via stdout. We could multiplex KV operations over this
channel. But this couples data storage to the agent process — scripts invoked
by the agent couldn't use it directly. A gRPC endpoint accessible to any
process in the container is more flexible.
