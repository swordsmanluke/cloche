# Subdirectory Layout, Daemon-Side Extraction, and Poll Command

**Date:** 2026-03-01

## Overview

Three related changes to Cloche:

1. Move cloche project files into a `./cloche/` subdirectory of the host project, separating host-level files (CLAUDE.md, .git, build config) from agent-level files.
2. Replace the in-container git push mechanism with daemon-side result extraction using `docker cp` and host-side git operations (with worktrees for concurrency).
3. Add a `poll` command that blocks until a run reaches a terminal state or its container has been dead for >1 minute.

## 1. Directory Layout

### Host project (user's repo root)

```
my-project/
├── .git/
├── .gitignore
├── .gitworktrees/         # Daemon-managed git worktrees (gitignored)
├── CLAUDE.md              # Host-level agent instructions
├── README.md
├── cloche/                # Everything cloche manages
│   ├── develop.cloche     # Workflow DSL file(s)
│   ├── Dockerfile
│   ├── CLAUDE.md          # Inner agent instructions
│   ├── .cloche/
│   │   ├── config
│   │   ├── prompts/
│   │   │   ├── implement.md
│   │   │   └── fix.md
│   │   ├── <run-id>/      # Per-run output (gitignored)
│   │   │   ├── prompt.txt
│   │   │   └── output/
│   │   └── ...
│   └── <source files>     # Project code the agent works on
└── <host-level files>
```

### Inside the container

```
/workspace/                # = contents of host's ./cloche/
├── develop.cloche
├── Dockerfile
├── CLAUDE.md
├── .cloche/
│   ├── config
│   ├── prompts/
│   └── ...
└── <source files>
```

The container sees a flat `/workspace/` — the contents of `./cloche/` are copied directly, not nested.

### Gitignore additions

`cloche init` adds to the host `.gitignore`:

```
cloche/.cloche/*/
.gitworktrees/
```

This ignores per-run output directories and worktrees, but keeps `cloche/.cloche/config` and `cloche/.cloche/prompts/` tracked.

## 2. Container Lifecycle and Result Extraction

### Run start

1. The daemon records `base_sha` = current HEAD of the host repo.
2. `base_sha` is stored in the run record (new column in SQLite `runs` table).
3. `docker cp <host>/cloche/. <container>:/workspace/`
4. Container starts; agent runs the workflow autonomously.

### Run completion

When the container exits (success or failure):

1. **Copy out:** `docker cp <container>:/workspace/. <tempdir>/`
2. **Create worktree:** `git worktree add --detach <project>/.gitworktrees/cloche/<run-id> <base_sha>`
3. **Populate:** Copy `<tempdir>` contents into `<worktree>/cloche/`
4. **Commit:**
   - `git checkout -b cloche/<run-id>` (in the worktree)
   - `git add cloche/`
   - `git commit -m "cloche run <run-id>: <workflow-name> (<result>)"`
5. **Cleanup:**
   - `git worktree remove <worktree-dir>`
   - Remove temp dir
   - Remove container (unless `--keep-container`)

### Concurrency

Each extraction uses its own git worktree at `<project>/.gitworktrees/cloche/<run-id>/`. Multiple extractions can run concurrently with no locking.

### Error handling

- If the container crashes, still attempt `docker cp` (the container filesystem persists until `docker rm`).
- If git operations fail, log the error, keep the container for debugging, and don't delete the temp dir.

## 3. Poll Command

### Usage

```
cloche poll <run-id>
```

### Behavior

1. Connects to daemon via gRPC.
2. Polls `GetStatus(<run-id>)` every 2 seconds.
3. Prints status changes as they happen:

```
[12:03:45] Run abc123 is running
[12:03:45] Step "implement" started
[12:04:12] Step "implement" completed: success
[12:04:12] Step "test" started
[12:04:30] Step "test" completed: fail
[12:04:30] Step "fix" started
[12:05:01] Step "fix" completed: success
[12:05:01] Step "test" started (retry)
[12:05:15] Step "test" completed: success
[12:05:15] Run abc123 succeeded
```

4. Exits when:
   - The run reaches a terminal state (succeeded, failed, cancelled), **or**
   - The associated container has been dead (or never started) for >1 minute.

### Exit codes

- `0` — run succeeded
- `1` — everything else (failed, cancelled, container dead)

### Protocol changes

Add to the `GetStatusResponse` proto:
- `bool container_alive` — whether the Docker container is currently running
- `google.protobuf.Timestamp container_dead_since` — when the container was last seen alive (if dead)

The daemon checks Docker container state when responding to status requests.

## 4. Changes to `cloche init`

`cloche init` now scaffolds into `./cloche/`:

1. Creates `./cloche/` directory.
2. Inside `./cloche/`:
   - `<workflow-name>.cloche`
   - `Dockerfile`
   - `CLAUDE.md` (agent instructions template)
   - `.cloche/config`
   - `.cloche/prompts/implement.md`
   - `.cloche/prompts/fix.md`
3. Adds `cloche/.cloche/*/` and `.gitworktrees/` to host `.gitignore`.

## 5. Changes to `cloche run`

- `project_dir` sent to daemon is the **host project root** (the directory containing `./cloche/`).
- The daemon looks inside `<project_dir>/cloche/` for workflow files, Dockerfile, etc.
- The daemon copies `<project_dir>/cloche/.` into the container's `/workspace/`.

## 6. What Gets Removed

1. **Git daemon** — `startGitDaemon()` / `stopGitDaemon()` in the Docker adapter. No longer needed.
2. **In-container git push** — Git setup, commit-tree, and push logic in `internal/agent/runner.go`.
3. **LLM-generated commit messages** — Commit message generation that happened inside the container.

## 7. Schema Changes

### SQLite `runs` table

Add column: `base_sha TEXT` — the commit SHA of HEAD at run start time.

### Protobuf `GetStatusResponse`

Add fields:
- `bool container_alive`
- `google.protobuf.Timestamp container_dead_since`
