# Resume: Rebuild Container and Preserve Workspace

**Status:** Final

## Overview

`cloche resume` currently reuses the original container image from the original run.
This prevents container or Dockerfile bug fixes from taking effect when resuming a
failed run. This document designs resume to rebuild the container fresh and re-apply
prior-run workspace changes so that fixes to the build environment are always picked
up on resume.

## Background

When a run fails mid-workflow, the user often fixes either:
- A bug in the project code (workspace change), or
- A bug in the Dockerfile or container setup.

The current behavior reuses the existing container, so Dockerfile fixes have no effect
until the user starts a brand-new run (losing the mid-run context of a resume).

## Workspace delta strategy

On every successful step completion, cloche snapshots the workspace diff relative to
the initial container image. This diff is stored as a tarball (or patch set) alongside
the run record. On resume, after the fresh container is built and started, cloche
re-applies the snapshot from the last successful step before dispatching the failed
step for retry.

The snapshot captures:
1. Modified and new files under `/workspace/` (compared to the initial overlay).
2. Deleted files (stored as a manifest of paths to remove).

Files outside `/workspace/` (e.g. installed packages) are intentionally excluded — the
fresh build is responsible for reproducing those from the updated Dockerfile.

## Conflict resolution

When re-applying the workspace snapshot to a freshly-built container, conflicts can
arise if the Dockerfile change also modifies files that the agent wrote during the
prior run. The resolution policy is:

- **Agent writes win by default.** The snapshot is applied after the build overlay,
  so the agent's changes take precedence over any Dockerfile-sourced defaults.
- **Explicit opt-out.** A step can declare `preserve_workspace = false` to skip
  snapshot re-application and start from a clean build state.
- **Merge conflicts in text files.** If a three-way merge is possible (original →
  Dockerfile change vs. original → agent change), cloche attempts it and writes the
  merged result. On unresolvable conflict the run fails with a descriptive error
  before the agent is invoked, prompting the user to resolve manually.

## Multi-attempt selection

A run can have multiple prior attempts (each partial attempt is a separate run record
sharing the same `task_id`). On `cloche resume <task-id>`:

1. The daemon selects the **most-recent attempt** that has at least one successful step.
2. If no attempt has a successful step, the run restarts from the beginning (no
   snapshot to replay).
3. The user can override with `--attempt <run-id>` to resume from a specific attempt's
   snapshot.

This ensures that progress is never silently discarded: the default is "resume from
as far as we got" and the override is "resume from this specific checkpoint."

## Rebuild: default vs opt-in

**Decision: rebuild is the default.** Rationale:

- Users who resume almost always want their Dockerfile fixes to take effect.
- The cost of an unnecessary rebuild is a few seconds; the cost of missing a fix is a
  confusing failure loop.
- The old behavior (reuse container) is available via `--no-rebuild` for situations
  where the container is expensive to build and the user is confident no image changes
  are needed.

Flag summary:

| Flag | Behavior |
|------|----------|
| *(none)* | Rebuild container, re-apply workspace snapshot |
| `--no-rebuild` | Reuse existing container, re-apply workspace snapshot |
| `--clean` | Rebuild container, start from clean workspace (no snapshot) |

## Implementation notes

The main entry points for implementing this design:

- **`Runner.ResumeRunAsNewAttempt`** (`internal/host/runner.go`) — host-side resume
  logic that creates the new attempt and drives the engine. This function requires no
  structural change; it already accepts a `resumeFrom` step name and delegates
  container management to the gRPC server layer.

- **`ContainerPool.CommitForResume`** (`internal/adapters/docker/pool.go`) — currently
  commits the failed container as a Docker image so the resumed run can reuse it. In
  the `--rebuild` path, **`CommitForResume` is not called**; instead, the snapshot
  captured at each step (see Workspace delta strategy above) is re-applied after the
  fresh container starts. This is the key behavioral fork between the existing resume
  path and the rebuild path.

### Sequencing the work

1. **Snapshot capture** — add a post-step hook in `internal/adapters/grpc/server.go`
   that `docker cp`s `/workspace/` to a per-attempt tarball after each successful step.
   Store the tarball path in the run record or a sidecar file under `.cloche/runs/`.

2. **`--rebuild` flag** — add `--rebuild` / `--no-rebuild` flags in `cmd/cloche/resume.go`.
   Pass the selection as gRPC metadata (new key `x-cloche-resume-rebuild`) alongside the
   existing `x-cloche-resume-run-id`.

3. **Server fork** — in the resume handler in `server.go`, read the rebuild flag from
   metadata. When rebuild is requested: skip `CommitForResume`, resolve the image from
   project config (not the committed image), start a fresh container via `SessionFor`
   with `ProjectDir` set, then inject the snapshot tarball into `/workspace/` after
   `AgentReady`.

4. **Snapshot injection** — new helper `injectWorkspaceSnapshot(ctx, containerID, tarPath)`
   uses `docker cp` to inject the tarball, mirroring the extraction in the opposite
   direction.

### Storage and retention

Workspace snapshots are stored as uncompressed tarballs. Compression can be added
transparently later if storage becomes a concern. Snapshots are retained until the
run record is deleted (same lifecycle as run logs and state files).

### Testing entry point

`internal/adapters/grpc/server_test.go` — add a `TestRebuildResume` scenario that
injects a pre-populated tarball and asserts it is applied to the container before the
engine dispatches the first step.
