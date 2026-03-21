# Interactive Console Design

**Date:** 2026-03-20
**Status:** Implemented (container runtime layer complete; gRPC/CLI pending)

## Problem

Cloche runs agents autonomously inside containers via workflows, but there is no
way to interactively work with an agent in a container environment. Users need
ad-hoc agent sessions — same Docker image, same project files, same auth setup —
without defining a workflow. Today the only option is manual `docker run` commands
that skip all of Cloche's container setup (project copy, auth files, overrides).

## Solution

Add `cloche console` — a CLI command that starts a fresh container from the
project's image, copies in the project files and auth credentials (same setup as
a workflow run), launches the agent command with a TTY, and forwards the user's
terminal I/O bidirectionally through the daemon via a streaming gRPC RPC. The
container is kept after the session ends (no auto-merge, no result extraction).
On exit, the CLI prints the container ID so the user can inspect or delete it.

## Design Details

### CLI Surface

```
cloche console [--agent <command>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--agent <command>` | Agent resolution chain | Override the agent command to run inside the container. |

Must be run from inside a git repository with a `.cloche/` directory (same as
`cloche run`). The daemon rebuilds the Docker image if `.cloche/Dockerfile` has
changed, same as for workflow runs.

**Behavior:**

1. CLI puts the terminal into raw mode.
2. CLI calls the `Console` gRPC RPC with project dir, agent command, and initial
   terminal size.
3. Daemon starts a container, attaches bidirectionally, and begins streaming.
4. User interacts with the agent. Stdin flows from CLI → daemon → container.
   Stdout/stderr flows from container → daemon → CLI.
5. Terminal resize signals (SIGWINCH) are forwarded as control messages.
6. When the agent exits (or the user sends Ctrl-C / Ctrl-D), the stream ends.
7. CLI restores the terminal and prints:
   ```
   Console session ended.
   Container: <container-id>
   ```

The container is always kept. The user can later inspect it with
`docker exec -it <id> bash`, extract files with `docker cp`, or delete it with
`cloche delete <id>`.

### gRPC Protocol

New bidirectional streaming RPC on `ClocheService`:

```protobuf
rpc Console(stream ConsoleInput) returns (stream ConsoleOutput);

message ConsoleInput {
  oneof payload {
    ConsoleStart start = 1;   // First message: config
    bytes stdin = 2;          // Subsequent: raw terminal input
    TerminalSize resize = 3;  // Terminal resize event
  }
}

message ConsoleOutput {
  oneof payload {
    ConsoleStarted started = 1; // First response: container info
    bytes stdout = 2;           // Subsequent: raw terminal output
    ConsoleExited exited = 3;   // Final: exit status
  }
}

message ConsoleStart {
  string project_dir = 1;
  string agent_command = 2;  // empty = use resolution chain
  uint32 rows = 3;
  uint32 cols = 4;
}

message ConsoleStarted {
  string container_id = 1;
  string run_id = 2;
}

message TerminalSize {
  uint32 rows = 1;
  uint32 cols = 2;
}

message ConsoleExited {
  int32 exit_code = 1;
}
```

The first `ConsoleInput` message is always a `ConsoleStart`. The daemon responds
with `ConsoleStarted` (containing the container ID), then begins streaming
output. Subsequent `ConsoleInput` messages carry raw stdin bytes or resize
events. The daemon sends raw stdout bytes and a final `ConsoleExited` when the
process terminates.

### Daemon Handler (`internal/adapters/grpc/server.go`)

The `Console` RPC handler:

1. Receives the `ConsoleStart` message.
2. Resolves the project directory and agent command (same resolution chain as
   workflow runs: flag → workflow config → `CLOCHE_AGENT_COMMAND` env →
   default `claude`).
3. Rebuilds the Docker image if needed (reuses existing `ensureImage` logic).
4. Creates the container with interactive + TTY flags (`-it`).
5. Copies project files, overrides, and auth credentials into the container
   (reuses `copyProjectToContainer` and auth copy logic).
6. Starts the container.
7. Attaches to the container via `docker attach` (stdin + stdout + stderr).
8. Sends `ConsoleStarted` with the container ID.
9. Spawns two goroutines:
   - **Input pump**: reads `ConsoleInput` from the gRPC stream, writes stdin
     bytes to the docker attach stdin, handles resize messages via
     `docker exec ... stty rows X cols Y` (or the Docker API resize endpoint).
   - **Output pump**: reads from the docker attach stdout, sends `ConsoleOutput`
     with raw bytes to the gRPC stream.
10. When the container process exits, sends `ConsoleExited` and closes the
    stream. Does **not** remove the container.

No run record is created in the database. No log indexing. No result extraction.
The console session is ephemeral from the daemon's perspective — it only
facilitates the connection.

### Container Runtime Changes (`internal/ports/container.go`)

`ContainerConfig` gains an `Interactive bool` field. When set, `Start` allocates
a TTY and keeps stdin open (`-it` flags). `ContainerRuntime` gains two additions:

```go
type ContainerConfig struct {
    // ... existing fields ...
    Interactive bool // allocate TTY and keep stdin open (-it flags)
}

// Attach connects to a running container's stdin/stdout/stderr for
// bidirectional I/O. Requires the container to have been started with
// Interactive=true in its ContainerConfig.
Attach(ctx context.Context, containerID string) (io.ReadWriteCloser, error)
```

Terminal resize is exposed as a separate optional interface so callers can
check for support at runtime:

```go
// TerminalResizer is an optional interface for resizing the pseudo-TTY of an
// interactive container. Used to forward SIGWINCH events from the CLI.
type TerminalResizer interface {
    ResizeTerminal(ctx context.Context, containerID string, rows, cols int) error
}
```

### Docker Adapter (`internal/adapters/docker/runtime.go`)

**`Start` changes:** When `cfg.Interactive` is true, add `-it` (interactive +
TTY) to the `docker create` arguments.

**New `Attach` method:**

```go
func (r *Runtime) Attach(ctx context.Context, containerID string) (io.ReadWriteCloser, error) {
    cmd := exec.CommandContext(ctx, "docker", "attach", "--sig-proxy=false", containerID)
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    cmd.Stderr = cmd.Stdout // merge stderr into stdout
    cmd.Start()
    return &attachConn{stdin: stdin, stdout: stdout, cmd: cmd}, nil
}
```

The `attachConn` struct implements `io.ReadWriteCloser` by delegating reads to
stdout and writes to stdin.

**Terminal resize:** Implemented as `ResizeTerminal` (satisfying the optional
`TerminalResizer` interface) via `docker exec <id> stty rows R cols C`. The
exec approach avoids adding the Docker Engine API client as a dependency.

### CLI (`cmd/cloche/main.go`)

New `cmdConsole` function:

1. Parse `--agent` flag.
2. Detect terminal size via `term.GetSize(os.Stdin.Fd())`.
3. Open `Console` gRPC stream and send `ConsoleStart`.
4. Receive `ConsoleStarted`, note the container ID.
5. Put terminal into raw mode (`term.MakeRaw`).
6. Spawn goroutines:
   - Read os.Stdin → send as `ConsoleInput{stdin: bytes}`.
   - Receive `ConsoleOutput{stdout: bytes}` → write to os.Stdout.
   - Listen for SIGWINCH → send `ConsoleInput{resize: ...}`.
7. On stream end or `ConsoleExited`, restore terminal.
8. Print container ID and exit with the agent's exit code.

Uses `golang.org/x/term` for raw mode and size detection (already a transitive
dependency via the Go standard library).

### Container Lifecycle

- Container is **always kept** after the session. No auto-removal.
- No run record in the database — `cloche list` does not show console sessions.
- User cleans up via `cloche delete <container-id>` or `docker rm <id>`.
- Container name follows the pattern `console-<short-id>` for easy
  identification in `docker ps`.

### Agent Command Resolution

Same chain as workflow runs:

1. `--agent` flag
2. Workflow-level `container { agent_command }` from the project's `.cloche`
   files (uses the first container workflow's config, since there is no
   specific workflow context)
3. `CLOCHE_AGENT_COMMAND` environment variable
4. Default: `claude`

The resolved command is run directly inside the container (not via
`cloche-agent`). No workflow parsing, no step execution, no result protocol.

### Error Handling

| Scenario | Behavior |
|----------|----------|
| Not in a git repo / no `.cloche/` | CLI error before connecting to daemon. |
| Docker image build fails | gRPC error returned, CLI prints and exits. |
| Container fails to start | gRPC error, CLI prints and exits. |
| Agent command not found in container | Container exits immediately with error, stream ends, CLI prints exit code. |
| Daemon unreachable | CLI connection error (same as other commands). |
| Network interruption mid-session | gRPC stream breaks, CLI restores terminal and prints error. Container keeps running; user can re-attach or delete. |
| Second `cloche console` to same container | Not applicable — each `console` starts a fresh container. |

## Alternatives Considered

**Attach to existing task containers.** Rejected because task containers run
autonomous workflows and injecting user input would interfere with the agent's
execution. Keeping console sessions separate avoids this conflict entirely.

**CLI talks to Docker directly (bypass daemon).** Rejected because the daemon
handles image building, project file copying, auth credential setup, and
`.clocheignore` filtering. Duplicating this in the CLI would be fragile and
drift over time.

**Create a run record for console sessions.** Rejected for simplicity. Console
sessions are ad-hoc and don't produce workflow results. Adding them to the run
database would clutter `cloche list` and require filtering everywhere. The
container ID is sufficient for cleanup.

**Support `--prompt` for initial input.** Rejected to keep the first version
simple. The user types everything interactively. A future version could support
piping initial input, but that's a different use case (scripted interaction).
