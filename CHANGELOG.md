# Cloche Changelog

## v3.15.1 — 2026-05-13

### Breaking changes

- Bot credential configuration for the agent image changed from bare SSH key files to an optional `gituser.toml` file. Migration: if you previously had `.cloche/credentials/id_ed25519` set up, create `.cloche/credentials/gituser.toml` with `name`, `email`, and `ssh_key` fields pointing to your existing key file (see `.cloche/setup-credentials.sh` for the schema).

### Notable fixes

- `make install` now succeeds on a fresh clone without requiring pre-existing credential files in `.cloche/credentials/`.

## v3.15.0 — 2026-05-13

### Features

- **Repository primitive** (cloche-em50). Declare `[[repositories]]` in `.cloche/config.toml`; workflows reference them via `repos = [...]`; steps pin a specific repo via `repository = "x"`. `cloche project` displays them; new `cloche project repos list` produces a machine-readable view. The container-building runtime will use the workflow's `repos` field to know which repositories to copy into `/workspace/<repo>/`.
- **Vertical development workflow** for layered feature delivery: `cloche run --workflow vertical` walks a feature through BDD test-plan → layered implementation (PR per layer) → docs → finalize. See `docs/design/vertical-workflow.md`.
- `verify-changes.sh` now runs `go build ./...` so workflow runs fail fast on non-compiling commits.
- New `[git]` config section (`name`, `email`, `ssh_key`) for per-project bot git identity; exports `CLOCHE_GIT_AUTHOR_NAME`, `CLOCHE_GIT_AUTHOR_EMAIL`, and `CLOCHE_GIT_SSH_COMMAND` to host scripts and uses them for extraction commits. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `cloche init` now offers an interactive SSH key setup flow and accepts `--non-interactive` / `--ssh-key <path>` flags; when the project has a GitHub remote, shows the direct URL for adding a deploy key. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `cloche doctor` now checks that the configured `[git] ssh_key` file exists and is readable (warning, not fatal).
- New `cloche debug goroutines` and `cloche debug state` subcommands for runtime introspection of the daemon; requires `cloched --debug-addr <addr>` or `CLOCHE_DEBUG` env var.

### Notable fixes

- `cloche stop` now synthesizes a `fail` result for the active step and walks fail-branch wires (e.g. `unclaim`) before the run transitions to `cancelled`.
- Step logs from in-flight steps are now flushed to disk on run teardown, so `cloche logs` returns output even when a run fails mid-execution.
- Workflow-level `container { image = "..." }` is now correctly used when dispatching sub-workflows via `workflow_name`; previously the daemon default was always used instead.
- `cloche shutdown --restart` now waits for the old daemon to exit before spawning a replacement, preventing two daemons from running simultaneously.
- Container startup failures now surface within ~2 minutes with diagnostic container logs, instead of blocking silently until the 30-minute step timeout.
- External directory and file symlinks in a project are now inlined in the container tar archive, preventing Docker tarslip protection from silently truncating the workspace.
- Step log files now accumulate across loop iterations instead of being overwritten on each pass, preserving the full history in `cloche logs`.
- Nested `.cloche/` project directories no longer cause the daemon to spawn duplicate orchestration loops that race over the same task queue.

## v3.14.21 — 2026-05-12

### Features

- Repository support: declare named source-code repositories in `[[repositories]]` config.toml entries; annotate them with remote URLs via top-level `repository "name" { ... }` blocks in `.cloche` files; reference them from workflows with `repos = ["name"]`. `cloche project` now shows a `Repositories:` section; `cloche project repos list` prints the repository table.

## v3.14.18 — 2026-05-05

### Features

- New `[git]` config section (`name`, `email`, `ssh_key`) for per-project bot git identity; exports `CLOCHE_GIT_AUTHOR_NAME`, `CLOCHE_GIT_AUTHOR_EMAIL`, and `CLOCHE_GIT_SSH_COMMAND` to host scripts and uses them for extraction commits. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `cloche init` now offers an interactive SSH key setup flow and accepts `--non-interactive` / `--ssh-key <path>` flags; when the project has a GitHub remote, shows the direct URL for adding a deploy key. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `cloche doctor` now checks that the configured `[git] ssh_key` file exists and is readable (warning, not fatal).
- New `cloche debug goroutines` and `cloche debug state` subcommands for runtime introspection of the daemon; requires `cloched --debug-addr <addr>` or `CLOCHE_DEBUG` env var.

### Notable fixes

- `cloche stop` now synthesizes a `fail` result for the active step and walks fail-branch wires (e.g., `unclaim`) before the run transitions to `cancelled`.
- Step logs from in-flight steps are now flushed to disk on run teardown, so `cloche logs` returns output even when a run fails mid-execution.
- Workflow-level `container { image = "..." }` is now correctly used when dispatching sub-workflows via `workflow_name`; previously the daemon default was always used instead.
- `cloche shutdown --restart` now waits for the old daemon to exit before spawning a replacement, preventing two daemons from running simultaneously.
- Container startup failures now surface within ~2 minutes with diagnostic container logs, instead of blocking silently until the 30-minute step timeout.
- External directory and file symlinks in a project are now inlined in the container tar archive, preventing Docker tarslip protection from silently truncating the workspace.
- Step log files now accumulate across loop iterations instead of being overwritten on each pass, preserving the full history in `cloche logs`.
- Nested `.cloche/` project directories no longer cause the daemon to spawn duplicate orchestration loops that race over the same task queue.

## v3.14.0 — 2026-04-15

### Breaking changes

- DEPRECATION: Wire output mapping syntax (`step:result -> next [ VAR = output.field ]`) has been removed. 
  **Migration**: use `cloche get/set` commands in place of `[ KEY = output.field ]` clauses on wire definitions.
- DEPRECATION: `step x { feedback = "true" }` the `feedback` Step config key has been removed. 
  **Migration**: to pass a preceding step's output into a prompt, use `{previous_output}` in prompt templates or read `$CLOCHE_PREV_OUTPUT` in script Steps.
- DEPRECATION: `CLOCHE_STEP_OUTPUT` is no longer set. 
  **Migration**: update scripts to print output directly to stdout rather than writing directly to the output capture file path.

### Features

- Added `cloche extract <id>` command to copy a container's `/workspace` to a git branch/worktree or a plain directory on the host. ([design](docs/plans/2026-04-14-cloche-extract-design.md))
- Added `changelog` and `release` host workflows for automated changelog generation and release tagging/publishing. ([design](docs/plans/2026-04-15-release-process-design.md))
- All binaries now accept `version` as a subcommand (`cloche version`, `cloched version`, `cloche-agent version`, `clo version`) in addition to `-v`/`--version`.
- `cloche logs` now supports compound step names of the form `subWorkflow:step` (e.g., `develop:implement`) to address individual steps within a sub-workflow's logs.
- `cloche init` now creates `prompts/`, `overrides/`, and `scripts/` subdirectories automatically.

### Notable fixes

- Container logs are now extracted from sub-workflow steps even when the parent context times out, preserving logs for post-mortem investigation.

