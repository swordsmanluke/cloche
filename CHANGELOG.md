# Cloche Changelog

## v3.18.2 — 2026-07-01

### Breaking changes

- Workflow blocks in `.cloche` files now require bare identifiers instead of quoted strings (`workflow name {` replaces `workflow "name" {`), aligning workflow names with the existing step and agent name syntax. Migration: in every `.cloche` file, change `workflow "name" {` to `workflow name {`. `workflow_name = "..."` inside step config and any prose references that use quoted names are unaffected.

### Features

- **`token-limit` config key.** Agent steps and workflows can now be bounded by an output-token ceiling to prevent runaway runs from consuming excessive budget. Declare `token-limit = N` on a step or workflow block; a step that exceeds its ceiling produces a `token-limit` result (implicit wire to `abort`); cumulative output across all steps is checked against the workflow-level ceiling. Per-step default is 500 000 output tokens; workflow default is 2 000 000; use `-1` to disable enforcement or `0` to abort immediately without running the step.

- **`cloche loop stop --hard`.** A new `--hard` flag on `cloche loop stop` parks all in-flight and resumable runs so they do not auto-resume on the next daemon restart. Plain `cloche loop stop` continues to halt new dispatch but leaves runs resumable; `cloche loop stop --hard` is the correct command before rebuilding or restarting the daemon. `cloche loop status` reports the count of parked runs.

- **`cloche resume` now rebuilds the container fresh and re-applies workspace state.** Resuming a failed run previously reused the exact container image from the original attempt, so Dockerfile fixes had no effect without starting a brand-new run. By default, `cloche resume` now rebuilds the container from the project image (picking up any Dockerfile changes) and re-applies the latest workspace snapshot from the failed attempt before the failed step is retried. Use `--no-rebuild` to keep the previous behavior (reuse committed container plus snapshot), or `--clean` to rebuild fresh without re-applying the snapshot. ([design](docs/plans/2026-05-28-resume-rebuild-preserve-workspace.md))

- **Vertical workflow: design-preparation phase (Phase 0.5).** When a feature requires architectural decision-making before implementation, the vertical workflow now runs a design stage that writes a design document, opens a pull request for human review, incorporates reviewer feedback, and records the approved design before proceeding to the BDD test-plan phase. ([design](docs/design/vertical-workflow.md))

- **Vertical workflow: no PR gates for implementation layers.** Layer, test-plan, and docs phases now push branches directly to origin and advance automatically without waiting for a GitHub pull request review; the `open-*-pr`, `poll-*-pr`, and `address-*-feedback` steps are removed from those phases. A layer that gets stuck fails immediately with a `document-stuck` report visible in `cloche logs`, rather than blocking on a stalled PR; `finalize` fast-forward-merges the rebased stack into the base branch and deletes the stack branches from origin. ([design](docs/design/vertical-workflow.md))

### Notable fixes

- Containers are now seeded from a clean `git archive` snapshot at baseSHA rather than from the live working tree. Host workflow steps check out branches in the project's shared working tree; seeding from that mutable directory caused stale or mid-checkout state to be committed into the container, which was then finalized back over `main` and repeatedly corrupted it. The daemon now materializes a clean snapshot via `git archive` before each container start and falls back to the live tree if the archive step fails (non-git directory, empty baseSHA, or archive error).

## v3.15.14 — 2026-05-21

### Notable fixes

- `{{ $task_id }}` now resolves correctly in agent prompts running inside host workflows; previously the host executor left it empty, breaking any prompt or shell command that embedded it (e.g. `bd show "{{ $task_id }}"`).

## v3.15.13 — 2026-05-21

### Breaking changes

- Inside `{{! }}` and `{{@ }}` directive bodies, `{{ $name }}` nested syntax no longer resolves; use bare `$name` instead. Migration: replace `{{! echo {{ $var }} }}` with `{{! echo $var }}` and `{{@ {{ $var }}.txt }}` with `{{@ $var.txt }}` in your prompt files.

## v3.15.12 — 2026-05-19

### Features

- Prompt templates: prompt files now support `{{ }}` directives — `{{ $name }}` for built-in variables and KV-store lookups, `{{! cmd }}` to inline shell output, and `{{@ path }}` to inline file contents. Expansion happens before the agent is invoked; any unresolvable directive fails the step early. Legacy `{task_description}` and `{previous_output}` placeholders continue to work with a deprecation warning. ([design](docs/plans/2026-05-18-prompt-templating-design.md))

## v3.15.10 — 2026-05-18

### Features

- Multi-repo extraction: container sub-workflows that declare `repos = [...]` now extract changes into per-repository worktrees and branches, with branch and path metadata stored per repo in the KV store. ([design](docs/plans/2026-04-14-cloche-extract-design.md))
- The streaming prompt adapter now supports opencode as a first-class agent command, parsing structured JSON events (text deltas, tool calls, and token usage) so agent steps using opencode produce complete `implement.log` output.

### Notable fixes

- Container workflows now correctly propagate `container { agent_command = ... }` and `container { agent_args = ... }` into step config instead of silently falling back to Claude.

## v3.15.9 — 2026-05-16

### Notable fixes

- Live log streaming and aggregation for nested host sub-workflow steps: inner step events now appear in `cloche logs -f` in real time, and their output is written to the parent run's `full.log` without a spurious `[script]` wrapper; container sub-workflow output is no longer duplicated in the live stream.

## v3.15.7 — 2026-05-15

### Breaking changes

- The `default` field in `[[repositories]]` config entries is removed; `cloche project repos list` now shows a `URL` column header instead of `FLAGS`. Migration: remove `default = true` from any `[[repositories]]` blocks in `.cloche/config.toml`; the default repository is now implicitly the single declared entry.

### Features

- New `skip` step config key: any step type (`agent`, `script`, `workflow`, `poll`, `human`) may declare a shell command that runs before the step executes; exit 0 bypasses the step and routes via the chosen wire (default `success`), non-zero runs the step normally; skipped steps appear as `skipped` in `cloche status` and do not count against `max_attempts`. ([design](docs/design/skip-scripts.md))
- `CLOCHE_TASK_ID`, `CLOCHE_RUN_ID`, `CLOCHE_ATTEMPT_ID`, and `CLOCHE_PROJECT_DIR` are now injected into agent process environments when an agent step runs inside a host workflow, enabling `cloche get`/`cloche set` calls from within those steps.
- `cloche project` now shows a deprecation warning with `[[repositories]]` migration instructions when no repository configuration is present in `.cloche/config.toml`; `ListRepositories` auto-seeds a root-path repository on first access for backward compatibility.

### Notable fixes

- `cloche project` (and `GetProjectInfo`) now correctly discovers host workflows from any `.cloche` file by inspecting the `host {}` block rather than treating only `host.cloche` as a source of host workflows.

## v3.15.1 — 2026-05-13

### Breaking changes

- Bot credential configuration for the agent image changed from bare SSH key files to an optional `gituser.toml` file. Migration: if you previously had `.cloche/credentials/id_ed25519` set up, create `.cloche/credentials/gituser.toml` with `name`, `email`, and `ssh_key` fields pointing to your existing key file (see `.cloche/setup-credentials.sh` for the schema).

### Notable fixes

- `make install` now succeeds on a fresh clone without requiring pre-existing credential files in `.cloche/credentials/`.

## v3.15.0 — 2026-05-13

### Features

- **Repository primitive** (cloche-em50). Declare `[[repositories]]` in `.cloche/config.toml`; workflows reference them via `repos = [...]`; steps pin a specific repo via `repository = "x"`. `cloche project` displays them; new `cloche project repos list` produces a machine-readable view. The container-building runtime will use the workflow's `repos` field to know which repositories to copy into `/workspace/<repo>/`.
- **Vertical development workflow** for layered feature delivery: `cloche run vertical` walks a feature through BDD test-plan → layered implementation (PR per layer) → docs → finalize. See `docs/design/vertical-workflow.md`.
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

