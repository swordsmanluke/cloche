# Cloche Bug Reports

Each entry captures the symptom, root cause (as far as investigation went), and
what a reasonable fix would look like.

---

## 1. Container stays in `Created` state; `SessionFor` blocks silently for the full step timeout

### Symptom

A host workflow dispatches a container sub-workflow (`step X { workflow_name = "develop" }`).
Expected: the container starts, the in-container agent connects back, the
`implement` step runs.

Observed: the container is created (visible via `docker ps -a`) but never
transitions out of `Created` state. No container logs. No cloched log output
after the `step_started: develop` line. The run appears completely hung for the
full 30-minute step timeout, then times out with no useful diagnostic.

Reproduced three times in a row on a clean Debian 12 host with Docker 24, on an
image derived from `cloche-base:latest`. Manually running `docker start <id>` on
the stuck container works fine — the container transitions to `running` and the
agent starts — so the image, create args, and project copy are all valid.

### Investigation

From `cloche/internal/adapters/docker/runtime.go`:

```go
// 5. Start the container (skip for interactive — Attach handles start).
if !cfg.Interactive {
    startCmd := exec.CommandContext(ctx, "docker", "start", containerID)
    var startStderr bytes.Buffer
    startCmd.Stderr = &startStderr
    if err := startCmd.Run(); err != nil {
        exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
        return "", fmt.Errorf("starting container: %s: %w", startStderr.String(), err)
    }
}
```

On error this path calls `docker rm -f`. Since the stuck container is still
present (not removed), `startCmd.Run()` must have either:

- Never been reached (the preceding copy-project / copy-auth steps hung
  silently), or
- Returned `nil` without actually starting the container (unlikely), or
- Been skipped because `cfg.Interactive` is unexpectedly true (checked — it's
  not set by `DaemonExecutor.executeContainerStep` in
  `internal/adapters/grpc/executor.go`).

The most likely explanation is that something in steps 2–5 of `runtime.Start`
(project copy, overrides copy, prompt write, auth copy) is hanging or silently
erroring on this host, and that the error path isn't actually cleaning up /
propagating. No log line from those steps exists.

### Suggested fix

1. **Log each sub-step of `runtime.Start`** (create, copy project, copy
   overrides, write prompt, copy auth, start) at info level with timing. Today
   if any of these hangs there's no way to tell which one from the outside.
2. **Verify the container transitioned to `running` after `docker start`**. A
   quick `docker inspect --format '{{.State.Status}}'` poll (a few tries over a
   second or two) would catch the `Created`-stuck case and surface a concrete
   error instead of returning success.
3. **Short-circuit on container exit.** If the container exits before
   `AgentReady` arrives, `SessionFor` should fail fast rather than waiting for
   the step timeout.

---

## 2. `SessionFor` has no dedicated timeout for `AgentReady`

### Symptom

When something goes wrong with container startup — e.g., the agent exits
immediately, the gRPC connection never forms, or the container is stuck in
`Created` (see #1) — `SessionFor` in
`cloche/internal/adapters/docker/pool.go:283-289` blocks until the step's
overall timeout fires:

```go
select {
case <-entry.readyCh:
    return sess, nil
case <-ctx.Done():
    return nil, fmt.Errorf("waiting for AgentReady for attempt %s: %w", attemptID, ctx.Err())
}
```

The default step timeout is 30 minutes. For startup failures that are
observable within seconds, 30 minutes of silence is a poor developer experience
and obscures the real problem.

### Suggested fix

Add a short `AgentReady` timeout (e.g., 60–120 seconds, configurable) distinct
from the step's overall timeout. If the agent hasn't registered within that
window, tear down the container, include the container's logs in the error
message, and return a fast failure.

Pair this with #1's status polling so the common "container never started"
case is detected in seconds rather than minutes.

---

## 3. ~~Workflow-level `container { image = "..." }` is ignored for `workflow_name`-dispatched sub-workflows~~ (FIXED)

**Fixed in:** `internal/adapters/grpc/executor.go` — `executeContainerStep` now reads `wf.Config["container.image"]` and falls back to the daemon default only when the workflow doesn't declare one.

### Symptom (historical)

`develop.cloche`:

```
workflow "develop" {
  container {
    image = "codemonkey-active:latest"
  }
  step implement { prompt = file(...); results = [success, fail] }
  ...
}
```

When `main` (host workflow) dispatches `develop` via `step develop {
workflow_name = "develop" }`, the container is launched with the **daemon
default image**, not `codemonkey-active:latest`. The `container { image = ...
}` block in the workflow file has no effect.

### Investigation

From `cloche/internal/adapters/grpc/executor.go:284-295`:

```go
cfg := ports.ContainerConfig{
    Image:        d.image,   // daemon-level default, NOT wf.Container.Image
    WorkflowName: wf.Name,
    ...
}
```

`d.image` is set once at executor construction from the daemon's default image
(`CLOCHE_IMAGE` env → `config.toml` `[daemon].image` → `cloche-agent:latest`).
The workflow's own container config is not consulted.

### Suggested fix

When dispatching a container sub-workflow, honor the workflow's
`container.image` if set, falling back to the daemon default only when the
workflow is silent. This matches reasonable expectation that the block is
actually used, and is consistent with the docs — `docs/workflows.md` says
`container {}` sets "container image, agent command, and network allowlist"
with no caveat that image is ignored for sub-workflows.

Supporting an env-var or KV reference inside the image string (e.g.
`image = "${IMAGE}"`) would also enable per-dispatch dynamic image selection
for projects that need it.

---

## 4. `cloche shutdown --restart` can leave two daemons running

### Symptom

After running `cloche shutdown --restart` (or `cloche shutdown -fr`), two
`cloched` processes are visible in `ps`:

```
erik     1049939 …  /home/erik/.local/bin/cloched        # original
erik     1061612 …  /home/erik/.local/bin/cloched        # spawned by --restart
```

Both daemons attach to the same project, both scan for active `.cloche/` configs,
and both attempt to orchestrate runs. This produces confusing behavior
(duplicate containers, one daemon claiming tasks the other is trying to process,
stale open FDs from the zombie daemon).

### Reproduction

On a host where the existing `cloched` is unresponsive or slow to shut down
(e.g., blocked in a goroutine that doesn't observe the shutdown signal —
see bug #1 scenarios), running `cloche shutdown --restart` launches the
replacement daemon without confirming the original actually exited.

### Suggested fix

`cloche shutdown --restart` should:

1. Send the shutdown signal.
2. Wait for the old process to actually exit (poll by PID with a reasonable
   timeout, e.g., 30s).
3. Only then spawn the replacement.
4. If the old daemon fails to exit within the timeout, report an error
   ("daemon X failed to shut down, not restarting") rather than spawning a
   second daemon on top.

A `--force-restart` that SIGKILLs the old process after the timeout would be a
reasonable addition for users who know they want the restart to succeed
regardless.

---

## 5. No way to scope `cloched` to a single project

### Symptom

`cloched` scans the watched path and starts an orchestration loop for every
nested `.cloche/` directory whose `config.toml` has `active = true`. There's
no CLI flag or config option to say "only run the loop for this one project."

This matters when a project contains vendored repos or sibling directories that
*also* ship with their own `.cloche/` configs. In that case multiple
orchestration loops run simultaneously, each scanning the same task queue and
each attempting to claim work. The result is races, confused state, and
containers being created by one loop while another is already processing the
same work item.

### Reproduction

1. Have a project `root/.cloche/config.toml` with `active = true`.
2. Inside that project, have another directory `root/some-sub/.cloche/config.toml`
   also with `active = true` (e.g., because it's a cloned repo that was
   previously cloche-managed).
3. Start `cloched`.
4. Observe two orchestration loops running via `cloche status` or the log.

### Suggested fix

A way to point the daemon at exactly one project path and ignore everything
else — either a `cloched --project <path>` flag, or `--no-auto-discover` that
disables the nested-scan behavior entirely. Less aggressive alternative: an
explicit allowlist in a top-level config file listing which `.cloche/` paths
should have loops.

---

## 6. No way to introspect `cloched`'s state while it's running

### Symptom

When `cloched` hangs or misbehaves, there is no runtime-introspection
mechanism. Diagnosing the state of goroutines, pending operations, container
sessions, or KV contents requires either:

- `SIGQUIT` the daemon (kills it; only gives a goroutine dump)
- Attach a debugger like delve (heavy, requires the daemon to be built with
  symbols and the user to have tooling installed)
- `strace` / `lsof` at the process level (shows syscalls and FDs but not Go-
  level state)

None of these are acceptable for a running production daemon where you want to
diagnose a live hang without taking down in-flight work.

### Reproduction

`cloched` hangs for any reason (see bug #1). Try to figure out which goroutine
is blocked on what without killing the daemon.

### Suggested fix

Expose some runtime-introspection surface. Options, roughly in order of effort
to add:

- **pprof HTTP endpoint.** Standard Go idiom; `net/http/pprof` gives goroutine
  dumps, heap profile, CPU profile, etc., over HTTP. Can be gated behind a
  `--debug-addr` flag that's off by default.
- **A CLI verb** like `cloche debug dump` that asks the daemon (over its
  existing gRPC surface) for a goroutine snapshot + summary of current
  sessions/loops.
- **Log rotation / verbose mode.** At minimum, a `--log-level debug` flag so
  you can collect detailed state transitions post-hoc without restarting.

---

## 7. `runtime.Start` hangs silently when the project root contains a symlink pointing outside the tree

### Symptom

A project directory contains a symlink at its root that points outside the
project tree (e.g. `./cloche -> ../Vendor/cloche/`). When a host workflow
dispatches a container sub-workflow, the container is created but never
transitions out of `Created` state. `cloched` produces no error output; no
docker subprocesses are running; the run eventually times out with no useful
diagnostic.

Removing the outward-pointing symlink from the project root and re-running
makes the container start normally. This was confirmed as the specific cause
of the hang described in bug #1 in our setup — with the symlink in place,
`copyProjectToContainer` stops partway through.

### Investigation

Inspecting the partially-created container (`docker diff <container>`) shows
that only the files that come alphabetically *before* the symlink are present.
Everything at or after the symlink is missing. No entry in the tar stream
exists for the symlink itself either.

`filepath.Walk` in `copyProjectToContainer` (cloche/internal/adapters/docker/runtime.go)
does not follow symlinks by default, so it should produce a symlink tar entry
and move on — but in this setup it either never produces the entry, or the
downstream `docker cp -` rejects/hangs on an outward-pointing symlink and the
writer side blocks on a full pipe buffer forever.

Docker is historically protective about tar entries containing symlinks whose
target escapes the archive root (to prevent tarslip). That defensive handling
may be silently failing the copy without surfacing anything on stderr that
`cloched` can read.

### Suggested fix

`copyProjectToContainer` should explicitly handle symlinks that point outside
the project tree. Options:

- Skip them (don't write them to the tar stream) and log a warning.
- Resolve them relative to `/workspace/` and only include if they resolve
  inside the project after extraction, otherwise skip.
- Surface any error from `docker cp` so the caller knows the copy failed
  instead of returning success with partial contents.

At minimum, the failure mode should not be a silent 30-minute hang.

---

## 8. Cancelling a task from the web UI should fail the current step so `unclaim` runs

### Symptom

When a task is cancelled from the web dashboard, the current step terminates
but the host workflow graph isn't walked further — in particular the
`unclaim` wire on the `fail` branch is not taken, so the tracker's work item
is left in the `Doing` / in-progress state. A human has to go fix up the
tracker state manually.

### Suggested behavior

Cancelling a task from the UI should translate to a `fail` result on the
currently-running step, which then naturally walks the existing `unclaim`
wire (if one is declared) the same way any other failure would. This keeps
the cancel path consistent with the workflow author's failure-handling graph
instead of short-circuiting it.

### Complication

(User note — complete this section: there's a complication around cancellation
behaviour when the step being cancelled is itself mid-RPC or mid-container
startup. Fill in the specifics.)

---

## 9. Step logs disappear after a run fails

### Symptom

While a container step is running, `cloche logs <attempt>:<workflow>:<step>`
(or the equivalent streamed output in the web dashboard) shows live step
output. The logs are visible and useful for watching progress.

After the run fails — whether by step-timeout, explicit failure, or cleanup —
those same logs are no longer retrievable. Querying the same ID returns either
an empty result or a "no run found" / "no logs" error, as if the step never
produced output at all.

### Reproduction

1. Dispatch a container workflow that will produce some stdout and then fail
   (or time out).
2. While it's running, run `cloche logs …` and confirm output is visible.
3. After the run completes with a failure, re-run the same `cloche logs …`
   command and observe that the output is no longer reachable.

### Suggested behavior

Failed runs should retain their captured logs. Retention for a failed run is
strictly more valuable than for a succeeded one — failure is when you need the
logs most, and losing them forces the user to reproduce the failure just to
see what happened. Whatever cleanup/pruning happens on run termination should
keep the per-step captured output intact for at least some retention window
(ideally indefinitely for failed runs, with an explicit eviction policy).

