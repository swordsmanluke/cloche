# `cloche extract` Command Design

**Date:** 2026-04-14
**Status:** Design

## Problem

Many Cloche workflows end up repeating the same pre-merge recipe: create a
branch and worktree on the host, wipe the branch's files, `docker cp` the
container's `/workspace` into the worktree, and replay the container's
commits as a squash on the host. The logic already exists in
`internal/adapters/docker/extract.go` (`ExtractResults`) but is wired to run
only daemon-side at container teardown — so workflow authors who want to
extract manually (for inspection, alternate merge strategies, or debugging)
have to re-implement it in bash.

## Solution

Add a `cloche extract <id>` CLI subcommand backed by a new `ExtractRun` gRPC
RPC. Refactor `ExtractResults` to take an `ExtractOptions` struct so it can
be parameterized for a custom target directory, a `--no-git` plain-copy
mode, and a "don't clean up the worktree" persist mode. Existing daemon-side
callers pass zero-valued options to preserve today's behavior. The CLI stays
thin — parse flags, call the RPC, print the result — keeping Docker access
inside the daemon per the hexagonal architecture.

## Design Details

### CLI surface

```
cloche extract <id> [flags]

  --at <dir>         Target directory (must be empty or not exist).
                     Default (git mode): .gitworktrees/cloche/<runID>.
                     Required when --no-git is set.
  --no-git           Skip worktree/branch/commit. Only docker cp the
                     container's /workspace into the target dir.
  --branch <name>    Branch name to create. Default: cloche/<runID>.
                     Ignored when --no-git.
```

`<id>` accepts the same forms as `cloche status` / `cloche logs`: run ID,
task ID (resolves to the task's latest attempt run), attempt ID, or
composite `task:attempt`. Reuse the existing resolver rather than
re-implementing.

On success, print:

```
Extracted to: <absolute-target-dir>
Branch: cloche/<runID>        # omitted when --no-git
```

### `ExtractOptions` refactor

File: `internal/adapters/docker/extract.go`.

Replace the positional signature with an options struct and typed result:

```go
type ExtractOptions struct {
    ContainerID  string
    ProjectDir   string
    RunID        string
    BaseSHA      string
    WorkflowName string
    Result       string

    // New fields — zero values preserve today's behavior.
    TargetDir string // "" → <ProjectDir>/.gitworktrees/cloche/<RunID>
    Branch    string // "" → cloche/<RunID>
    NoGit     bool   // skip worktree/branch/commit; only docker cp
    Persist   bool   // skip the defer-remove of the worktree
}

type ExtractResult struct {
    TargetDir string
    Branch    string // empty when NoGit
    CommitSHA string // empty when NoGit
}

func ExtractResults(ctx context.Context, opts ExtractOptions) (ExtractResult, error)
```

Core flow changes:

- **`TargetDir`**: when empty, fall back to today's
  `<ProjectDir>/.gitworktrees/cloche/<RunID>`. When non-empty, enforce the
  empty-or-nonexistent contract before any docker work.
- **`Branch`**: when empty, fall back to `cloche/<RunID>`.
- **`NoGit`**: if true, skip worktree, wipe-and-replace, branch, and
  commit. Only run `docker cp <ContainerID>:/workspace/. <TargetDir>/`.
  `BaseSHA` is not required in this mode.
- **`Persist`**: if false, keep today's `defer git worktree remove --force`.
  If true, skip it so the worktree sticks around.

Existing callers at `server.go:1450` and `executor.go:243` pass only the old
fields; behavior unchanged.

### gRPC RPC

File: `api/proto/cloche/v1/cloche.proto`.

```proto
rpc ExtractRun(ExtractRunRequest) returns (ExtractRunResponse);

message ExtractRunRequest {
  string id     = 1; // run / task / attempt / composite
  string at_dir = 2; // optional override
  string branch = 3; // optional override
  bool   no_git = 4;
}

message ExtractRunResponse {
  string target_dir = 1;
  string branch     = 2; // empty when no_git
  string commit_sha = 3; // empty when no_git
}
```

Regenerate `api/clochepb/*.pb.go` after editing the proto.

### Server handler

File: `internal/adapters/grpc/server.go`.

The handler parallels `DeleteContainer` (line 2806) for ID resolution:

1. Resolve `req.Id` to a `*domain.Run`, trying run ID → task ID → attempt
   ID in the same order the status command uses.
2. Validate:
   - `run.ContainerID != ""` — else "run %q has no associated container".
   - Container still exists via `s.container.Inspect` (precedent at
     `server.go:1163`). On miss, error with
     `"container for run %s has been removed; re-run with --keep-container"`.
   - If `!req.NoGit`: `run.BaseSHA != ""` — else the extract has no anchor.
3. Pre-check collision (before any docker cp):
   - Resolved `TargetDir` must be nonexistent or empty.
   - If `!NoGit`: the branch must not already exist
     (`git show-ref --verify refs/heads/<branch>`).
   - Collisions return a clear error — no `--force` flag in v1.
4. Call `docker.ExtractResults` with `Persist: true` plus the resolved
   target / branch / no-git.
5. Running container is **allowed**. `docker cp` works on live containers
   and produces a snapshot — no special gating.

Return `ExtractRunResponse` populated from the returned `ExtractResult`.

### CLI

New file: `cmd/cloche/extract.go`, in the per-command style of `poll.go` and
`console.go`. Function `cmdExtract(ctx, client, args)`:

- Parse flags (`--at`, `--no-git`, `--branch`) with the existing
  `main.go:205+` loop pattern.
- Enforce `--no-git` requires `--at` at the CLI — friendlier than letting
  the server reject it.
- Call `client.ExtractRun`, print target dir and branch.

Wire into `cmd/cloche/main.go`:

- Add `"extract": true` to the `daemonCmds` map (line 143).
- Add `case "extract": cmdExtract(ctx, client, os.Args[2:])` in the dispatch
  switch (line 170).
- Add an entry to `printTopLevelHelp` and to `subcommandHelp` in
  `cmd/cloche/help.go`.

### Error Handling

- Run not found → `cloche extract: no run for id %q`.
- Container already removed (common for succeeded runs without
  `--keep-container`) → `cloche extract: container for run %s has been
  removed; re-run the workflow with --keep-container to retain it`.
- Missing BaseSHA in git mode → `cloche extract: run %s has no base SHA
  recorded; use --no-git to extract files only`.
- Target dir not empty → `cloche extract: target %q is not empty`.
- Branch collision → `cloche extract: branch %q already exists`.
- Running container → no error; snapshot proceeds.

## Alternatives Considered

- **CLI drives Docker directly.** CLI calls `GetStatus` for the container
  ID, then runs `docker cp` and git itself. Rejected: leaks Docker access
  into the CLI, requires `docker` on every CLI host, scatters extract logic
  across two call sites. Breaks the "CLI is a thin gRPC client" invariant
  `cmd/cloche/main.go` maintains today.
- **Hybrid: small RPC returns container ID; CLI does the rest.** Adds an
  RPC _and_ still ships Docker code into the CLI. Worst of both worlds.
- **`--force` flag to overwrite existing target/branch.** Deferred — a
  clear error message is usually what the user wants. `--force` can be
  added in a follow-up if needed.
- **Gate on running container.** Considered refusing extraction on
  in-progress runs to avoid mid-step snapshots. Rejected because snapshots
  of a live container are genuinely useful for debugging, and `docker cp`
  on a running container is well-defined.

## Out of Scope

- `--force` to replace an existing target/branch.
- `--base <sha>` override for `Run.BaseSHA`.
- `cloche extract --list` to enumerate on-disk extractions.
- Consolidating `.cloche/scripts/cleanup-run.sh`'s worktree cleanup into
  this same machinery.

## Verification

**Unit tests** — extend `internal/adapters/docker/extract_test.go`:

- Default options preserve today's behavior.
- `TargetDir` override lands the worktree at the specified path.
- `NoGit: true` produces only files, no `.git`, no branch ref.
- `Persist: true` leaves the worktree in place after return.
- Nonempty target pre-condition returns an error.
- Branch collision pre-condition returns an error (git mode only).

Mock `docker cp` via a package-private hook
(`var dockerCp = realDockerCp`) that tests override with a local `cp -a`
from a fixture dir.

**gRPC tests** — `internal/adapters/grpc/server_test.go`:

- Valid run returns a populated `ExtractRunResponse`.
- Nonexistent ID → NotFound-shaped error.
- Run with empty `ContainerID` → error mentioning container removal.
- `NoGit: true` with `BaseSHA: ""` → succeeds.
- `NoGit: false` with `BaseSHA: ""` → error.

**CLI tests** — new `cmd/cloche/extract_test.go` mirroring `poll_test.go`:

- Flag parsing for `--at`, `--no-git`, `--branch`.
- `--no-git` without `--at` fails before dialing the daemon.
- Printed output includes target dir and branch.

**End-to-end** — with a real daemon:

- Run a small workflow with `--keep-container`, then:
  - `cloche extract <runID>` — default path + branch exist, files match
    container.
  - `cloche extract <runID> --at /tmp/x --branch fix/foo` — overrides
    honored.
  - `cloche extract <runID> --no-git --at /tmp/y` — plain copy, no `.git`.
  - `cloche extract <runID>` twice — second call fails with "target not
    empty" / "branch exists".
- If `test/regression/testenv.go` already runs real containers, add a
  regression case covering the above.
