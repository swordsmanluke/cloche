# Cloche Detailed Changelog

## v3.18.2 — 2026-07-01

### Breaking

- `e663576` DSL: workflow blocks require bare identifiers (`workflow name {` instead of `workflow "name" {`). Migration: remove quotes from the workflow name in all `.cloche` files; `workflow_name = "..."` step fields are unaffected.
- `0631f26` Re-applies the above DSL identifier change on main after it had been on a merged feature branch.
- `4f5ec20` Re-applies the DSL identifier change a second time after a vertical-layer squash commit (`5e3fccd`) accidentally reverted it; also re-patches `.cloche` scaffold files, all test fixtures, and examples.

### Features

- `389bfda` Add `token-limit` config key to DSL and domain layer (L1): parser support, domain validation, implicit `token-limit → abort` wires on every step, workflow-level shorthand wire, and `docs/workflows.md` reference section.
- `835e557` Token-limit L2: engine enforcement for per-step output-token ceilings and cumulative workflow ceiling; `token-limit = 0` short-circuit aborts without invoking the executor; defaults 500 000/step and 2 000 000/workflow; `-1` disables.
- `b7622b2` Loop resume gate L1: add `QuiesceRuns` gRPC RPC to the proto and a stub server handler; wire `cloche loop quiesce` subcommand and `--quiesce` flag on `cloche loop stop` to the new RPC.
- `5a24b73` Loop resume gate L2: implement `QuiesceRuns` RPC to mark resumable runs as parked in the SQLite store; `cloche loop status` reports the parked count; BDD step definitions implemented.
- `a20cbbb` Rename operator surface from `cloche loop quiesce` / `--quiesce` to `cloche loop stop --hard`; help text, shell completion, and BDD scenarios updated; the underlying `QuiesceRuns` gRPC wire is unchanged.
- `550201f` Resume rebuild infrastructure: tar-stream workspace snapshot capture/inject helpers (`internal/adapters/grpc/snapshot.go`), design doc, and Docker pool `CommitForResume` helper.
- `d74ceb9` Resume rebuild: `--no-rebuild` / `--clean` flags on `cloche resume`; server-side rebuild fork that defaults to rebuilding the container fresh and re-applying the latest workspace snapshot; `ensureResumeImage` hook for runtimes that support image rebuilding.
- `5e3fccd` Vertical workflow: add design-preparation phase (Phase 0.5) with scripts (`vertical-prepare-design-branch.sh`, `vertical-open-design-pr.sh`, `vertical-record-design.sh`) and stub prompts for design doc authoring, PR open/review, and feedback addressing; also extends `.cloche/vertical.cloche` and removes the legacy PR-gate steps from implementation layers. Note: this commit also accidentally reverted the DSL identifier change (fixed by `4f5ec20`).
- `546a93a` Vertical workflow: expand and refine the design-prep prompts (`vertical-write-design.md`, `vertical-address-design-feedback.md`, `vertical-check-design-needed.md`) and scripts (`vertical-finalize.sh`, `vertical-open-design-pr.sh`); implement `vertical_design_prep_test.go` BDD step definitions.
- `c61eef4` BDD scenarios self-register via `func init() { registerScenarios(...) }` into a package-level registry; `TestMain` iterates the registry instead of a hardcoded list, so concurrent feature branches no longer conflict on the same line.

### Fixes

- `34e44f5` Containers are now seeded from a `git archive` snapshot at baseSHA rather than the live working tree, preventing host-workflow branch checkouts from corrupting subsequent container seeds and causing `finalize` to write stale state back to `main`.
- `317a722` Fix `token_limit.feature` DSL inline snippets to use bare identifier workflow names after the parser change in `0631f26`.
- `e68ad53` Fix three test failures introduced by the token-limit L1 layer (`389bfda`).

### UI/UX

- `8a5358f` Update GitHub repository URLs in `docs/INSTALL.md` and `docs/USAGE.md` from `swordsmanluke/cloche` to `cloche-dev/cloche` after the GitHub org rename.

### Internal

- `b7f431d` Add `docs/run-isolation/` guide: overview index, architecture walkthrough with implementation anchors, and D2 source + SVG diagrams for the per-run lifecycle and finalize flow.
- `428b1f1` Squash: vertical update-docs pass (`6hcr-vertical-update-docs`); documentation updates for recently-landed vertical workflow changes.
- `1ffd322` No-op squash placeholder (`6hcr-implement-vertical-layer`, second attempt); empty diff, no file changes.
- `89111e3` Add design docs `docs/plans/2026-05-28-run-state-step-view.md` and `docs/plans/2026-05-28-step-token-metrics.md`; expand `run_state_step_view_test.go` with L2 BDD stub step definitions.
- `9ba4db2` Add BDD test plan for the run-state per-step view design doc.
- `a532c9c` Add BDD test plan for the per-step token metrics design doc.
- `5e99727` Tests for workspace snapshot helpers, rebuild-mode predicate helpers (`modeUsesCommit`, `modeUsesSnapshot`, `shouldCaptureSnapshot`), and `parseResumeFlags` flag parsing.
- `c72ce8f` Update `docs/design/vertical-workflow.md`: document the drop-PR-gates and fail-stop-stuck-layers changes (more complete pass).
- `c92b72b` Update `docs/design/vertical-workflow.md`: document drop-PR-gates and fail-stop-stuck-layers changes (earlier pass).
- `2d43cea` Add BDD test plan for vertical workflow design-prep stage (Phase 0.5).
- `e999c53` Refactor `theTokenLimitDSLFileIsParsed` to use early returns for readability.
- `bb2c806` Extract `validateTokenLimit` helper to eliminate DRY violation identified in self-review.
- `ac42e25` Add BDD test plan for extract-base-SHA re-resolution feature (`features/extract_base_sha_reresolution.feature`); subsequently removed by `5e3fccd`.
- `b461e14` Merge pull request #36 (token-limit test plan branch into vertical stack).
- `33a092b` Squash: BDD test plan for token-limit config feature (`cavf-vertical-bdd-test-plan`).
- `2fc4cc6` Merge pull request #19 (vertical test-plan branch into main).
- `57d3f60` Add BDD test plan for token-limit config feature (`features/token_limit.feature`, `features/token_limit_test.go` stubs).
- `3ebdba3` Add BDD test plan for loop resume gate.
- `982a18e` Add BDD test plan for run-state per-step view design doc (earlier attempt, superseded by `9ba4db2`).
- `50c0f2c` Docs: vertical test-plan/docs phases are now idempotent on re-dispatch (more complete pass).
- `44e7664` Docs: vertical test-plan/docs phases idempotent on re-dispatch (earlier pass).
- `240be52` Docs: DSL identifier workflow names reference, step-token-metrics design, and `{{@ }}` fix notes (more complete pass).
- `111becd` Docs: DSL identifier workflow names, step-token-metrics design, `{{@ }}` fix (earlier pass).
- `e42286b` Docs: `token-limit` config key in DSL reference (`docs/workflows.md`) and usage guide.
- `65a9ffe` Docs: loop resume gate with `cloche loop quiesce` terminology (later superseded by `--hard` rename).
- `7ff70f0` Docs: loop resume gate (earlier pass).
- `08d6a37` Recovery: restore main to commit a131835 content (second stale-finalize reversion).
- `ebe9b2a` Recovery: restore main to commit a131835 (first stale-finalize reversion after a whole-tree corruption).
- `c6041a7` Version bump to 3.18.2.
- `a61e535` Version bump to 3.18.1.
- `ef5795d` Version bump to 3.18.0.
- `01291cf` Version bump to 3.17.0.

## v3.15.14 — 2026-05-21

### Fixes

- `6186e9a` `{{ $task_id }}` now resolves correctly in agent prompts inside host workflows; the host executor was assigning `adapter.RunID` but omitting `adapter.TaskID`, leaving the variable empty for any agent step in a host workflow.

## v3.15.13 — 2026-05-21

### Breaking

- `7c7139a` Prompt template directive bodies now resolve bare `$name` references (built-in or KV) instead of full `{{ $name }}` nested directives; `{{` and `}}` characters inside a body are literal and pass through to the shell or file path verbatim; the parser still depth-balances `{{`/`}}` to locate the true outer closing pair. Migration: replace any `{{ $var }}` inside `{{! }}` or `{{@ }}` bodies with bare `$var`. ([design](docs/plans/2026-05-18-prompt-templating-design.md))

## v3.15.12 — 2026-05-19

### Features

- Prompt files now support `{{ }}` template directives evaluated before the agent is invoked. Three forms: `{{ $name }}` (built-in variable or KV-store lookup), `{{! cmd }}` (sh -c; stdout substituted, stderr to step log, 30 s timeout, non-zero exit fails step), `{{@ path }}` (file contents substituted verbatim). Inner `{{ $var }}` references inside shell and file directives resolve before the outer directive executes. `$$` → `$` inside shell directives only. Unresolvable directives fail the step before the agent runs; error messages name the directive and cause. ([design](docs/plans/2026-05-18-prompt-templating-design.md))

### Internal

- Legacy `{task_description}` and `{previous_output}` placeholders now emit a per-step deprecation warning through the status writer; the substitution itself is unchanged.

## v3.15.10 — 2026-05-18

### Features

- `8aa5783` The streaming prompt adapter now supports opencode as a first-class agent command, parsing `--format json` events (text deltas, tool_use, step_finish) and extracting token usage.
- `45f6dde` Multi-repo extraction: container sub-workflows that declare `repos = [...]` now extract changes into per-repository worktrees and branches, with per-repo branch and path metadata written to the KV store. ([design](docs/plans/2026-04-14-cloche-extract-design.md))

### Fixes

- `2e5fb43` Container workflows now correctly propagate `container { agent_command = ... }` and `container { agent_args = ... }` into step config instead of silently falling back to Claude.

### Internal

- `d8a1f14` Extend godoc on `rollbackWorktrees` to document error handling.
- `c11a229` Update documentation references from `cloche run --workflow <name>` to `cloche run <name>` across README, USAGE, and design docs.

## v3.15.9 — 2026-05-16

### Fixes

- `7fa9315` Live log streaming and aggregation for nested host sub-workflow steps: `innerHostStatusHandler` now broadcasts inner step start/complete events to the parent run's log broadcaster so `cloche logs -f` reflects them in real time; `aggregateHostSubWorkflowLogs` concatenates per-step log files into a single `<step>.log` so the outer `full.log` receives them; `logstream.Writer.Append` writes pre-formatted log content without adding a `[script]` type wrapper; `hostStatusHandler.OnStepComplete` no longer re-broadcasts batch output for workflow steps (which was already streamed live by the inner handler or container `StepLog` messages), preventing duplicate lines.

## v3.15.7 — 2026-05-15

### Breaking

- `cf17793` The `default` field is removed from `[[repositories]]` config entries and from the `Repository` proto message (field 4 is now reserved to prevent future reuse); `cloche project repos list` column header changed from `FLAGS` to `URL`. Migration: remove `default = true` from `[[repositories]]` blocks in `.cloche/config.toml` — the field is silently ignored at parse time but has no effect; the implicit default repository is now the single declared entry when exactly one is configured.

### Features

- `5c08f33` New `skip` step config key accepted on any step type: a shell command run before the step with a 90 s timeout; exit 0 bypasses the step (routing via `success` or a `CLOCHE_RESULT:<wire>` marker on stdout), non-zero exits run the step normally; skip output is captured to `step.<name>.skip.log`; skipped steps appear as `skipped` in `cloche status`/`cloche list` and do not increment the `max_attempts` counter. ([design](docs/design/skip-scripts.md))
- `5eb58f8` Host-workflow agent steps now receive `CLOCHE_TASK_ID`, `CLOCHE_RUN_ID`, `CLOCHE_ATTEMPT_ID`, and `CLOCHE_PROJECT_DIR` as environment variables via the prompt adapter's new `ExtraEnv` field, enabling `cloche get`/`cloche set` from within host-workflow agent steps.
- `1fe0cd8` `cloche project` emits a `DEPRECATED:` warning with `[[repositories]]` migration instructions when no repository configuration is found in `config.toml`; `ListRepositories` auto-seeds a single entry (name = project directory basename, path = project root) on first access for projects with no stored repository rows.

### Fixes

- `893a00e` `GetProjectInfo` now uses `dsl.ParseAll` on every `.cloche` file and routes workflows to host or container by inspecting each workflow's `host {}` block; previously only `host.cloche` was parsed for host workflows and other files were always treated as container-workflow sources, causing misclassification for projects that define host workflows outside `host.cloche`.

### Internal

- `893a00e` Added `docs/design/skip-scripts.md` design document describing the skip-scripts feature (semantics, DSL, lifecycle, protocol, implementation surface).

## v3.15.1 — 2026-05-13

### Breaking

- `27d300a` Bot credential setup for the agent image changed from hard-required bare SSH key files to an optional `gituser.toml`-driven scheme (`name`/`email`/`ssh_key` fields); `make install` now creates an empty `.cloche/credentials/` placeholder so the build works on fresh clones. Migration: if you previously had `.cloche/credentials/id_ed25519` configured, create `.cloche/credentials/gituser.toml` referencing it (see `.cloche/setup-credentials.sh` for the full schema).

## v3.15.0 — 2026-05-13

### Features

- `45a71ff` / `2402097` / `714cac1` Adds the **Repository** primitive (cloche-em50). A project's `.cloche/config.toml` may now declare `[[repositories]]` entries with `name`, `path`, and `url` fields. Workflows declare which repos they consume via a top-level `repos = ["a", "b"]` field, and individual steps may pin a specific repo via `repository = "x"`. The proto's `Repository` message and `GetProjectInfoResponse.repositories` field expose the loaded set; `cloche project` renders a Repositories section, and `cloche project repos list` produces a machine-readable listing. The previously-prototyped top-level `repository "name" { }` DSL block was deliberately not shipped — repositories are declared only in `config.toml`. (Followup tickets: `cloche-yn27` to remove the `default` field in favor of a single-entry implicit default; `cloche-i6xn` to land the deprecation-warning and DB auto-seed BDD scenarios; `cloche-8m3c` to restore the SetContextKey 1KB cap.)
- `45b1238` (+ many follow-ups) Adds the **vertical development workflow** for layered feature delivery: `cloche run vertical` walks a feature task through BDD test-plan, layered implementation (each layer becomes its own PR), docs, and finalize phases. Each phase opens a PR the user must approve before the next phase begins. See `docs/design/vertical-workflow.md`.
- `c055b94` `verify-changes.sh` (used by `develop` and `vertical` workflows) now runs `go build ./...` after the changes check, so workflows fail fast on commits that don't compile.
- `05df2ec` Adds `[git]` config section with `name`, `email`, and `ssh_key` fields; exports `CLOCHE_GIT_AUTHOR_NAME`, `CLOCHE_GIT_AUTHOR_EMAIL`, and `CLOCHE_GIT_SSH_COMMAND` to host scripts and uses the resolved identity for extraction commits. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `7128952` `cloche init` now prompts for SSH key setup interactively; adds `--non-interactive` flag to skip all prompts and `--ssh-key <path>` to write `ssh_key` into `.cloche/config.toml` non-interactively.
- `eea6192` `cloche init` SSH key setup now detects the project's GitHub remote origin and shows the direct deploy-key settings URL (`github.com/<owner>/<repo>/settings/keys`) when prompting for key generation.
- `b583c29` `cloche doctor` now verifies that the configured `[git] ssh_key` file exists and is readable, loading the merged global + project config; reports a warning (not a failure) when the file is missing.
- `e54c52f` New `cloche debug goroutines` and `cloche debug state` subcommands expose the running daemon's goroutine stacks, active run IDs, orchestration loops, and container session state; requires `cloched --debug-addr <addr>` or `CLOCHE_DEBUG=<addr>` or `[daemon] debug` in global config.

### Fixes

- `a5c63e3` When the outer workflow context is cancelled (e.g. via `cloche stop`), the engine now synthesizes a `fail` result for the active step and walks fail-branch wires (e.g. an `unclaim` step) before marking the run `cancelled`.
- `091673c` Broadcaster history is flushed to disk (as `full.log` and per-step logs) when a run is torn down, ensuring `cloche logs` returns output for runs that failed mid-execution with no on-disk log yet.
- `8152eef` Workflow-level `container { image = "..." }` is now read when dispatching a container sub-workflow via `workflow_name`, overriding the daemon default; also adds `child_branch` to the auto-seeded KV store (set before the sub-workflow runs) so merge scripts can reference the extracted branch name before `child_run_id` is available.
- `ca58e67` `cloche shutdown --restart` now polls the daemon address until the old process stops accepting connections before launching the replacement, preventing two daemons from running simultaneously.
- `2185d67` `SessionFor` now has a dedicated 2-minute AgentReady timeout; when exceeded, the container is stopped and its logs are included in the returned error, replacing the previous behavior of blocking until the step's 30-minute timeout.
- `4d6f87f` After `docker start`, `runtime.Start` polls until the container reaches `Running` state and returns an error if it does not transition; a background goroutine also watches for early container exit so `SessionFor` fails fast with logs rather than waiting the full ready timeout; `runtime.Start` now logs each sub-phase with wall-clock timing.
- `46f0158` External directory and file symlinks in the project are now inlined as regular entries in the tar archive sent to the container, preventing Docker tarslip protection from silently dropping them and leaving the workspace incomplete.
- `7ff07bb` External symlinks nested inside an already-dereferenced external directory are now recursively inlined rather than emitted as symlink entries that Docker's tarslip guard would reject.
- `0d1d24b` `addDereferencedEntry` now returns an error when an external symlink's target is inaccessible (previously it printed a warning to stderr and silently continued, producing an incomplete workspace).
- `fdb3d32` `copyProjectToContainer` now closes the tar pipe with an error on walk failure (so `docker cp` receives a broken stream and exits non-zero) and treats any `docker cp` stderr output as an error even when the process exits 0.
- `f62b7bf` The daemon now rejects `EnableLoop` requests for project directories nested inside an already-active loop's scope, and stops superseded child loops when a parent loop is enabled; startup deduplication also filters nested paths.
- `02dee9e` Step log files are now opened in append mode in the generic and prompt adapters, and the session and host status handler track per-step byte offsets so only new output is written to `full.log` per loop iteration.

### Internal

- `ef9ad29` Added `cloched --project` flag to scope the daemon to a single project directory.
- `414760e` Reverts the `--project` flag added in `ef9ad29` (the approach was wrong; the correct fix is loop deduplication, implemented in `f62b7bf`).
- `2c3b541` Release publish script now unsets `GITHUB_TOKEN` before pushing to avoid using the environment token instead of the configured SSH key.

## v3.14.21 — 2026-05-12

### Features

- Adds `[[repositories]]` array-of-tables section to `config.toml` for declaring named source-code repositories (`name`, `path`, `default` fields). Loaded by a new `internal/project` package into `domain.Project`.
- Adds top-level `repository "name" { path, url, default }` block to the `.cloche` DSL. `ParseRepositoriesFrom` reads repository blocks from a file; `ParseAll` silently skips them so existing workflow parsing is unaffected.
- Adds `repos = ["name", ...]` workflow-level field to the DSL, stored in `domain.Workflow.Repos`. Documents which repositories a workflow depends on.
- `cloche project` now includes a `Repositories:` section listing each repository's name, path, URL, and default flag when repositories are declared. New `cloche project repos list` subcommand prints the repository table in isolation.
- Adds `Repository` proto message to `GetProjectInfoResponse` (field 16); repositories are returned by the `GetProjectInfo` gRPC RPC.

## v3.14.18 — 2026-05-05

### Features

- `05df2ec` Adds `[git]` config section with `name`, `email`, and `ssh_key` fields; exports `CLOCHE_GIT_AUTHOR_NAME`, `CLOCHE_GIT_AUTHOR_EMAIL`, and `CLOCHE_GIT_SSH_COMMAND` to host scripts and uses the resolved identity for extraction commits. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `7128952` `cloche init` now prompts for SSH key setup interactively; adds `--non-interactive` flag to skip all prompts and `--ssh-key <path>` to write `ssh_key` into `.cloche/config.toml` non-interactively.
- `eea6192` `cloche init` SSH key setup now detects the project's GitHub remote origin and shows the direct deploy-key settings URL (`github.com/<owner>/<repo>/settings/keys`) when prompting for key generation.
- `b583c29` `cloche doctor` now verifies that the configured `[git] ssh_key` file exists and is readable, loading the merged global + project config; reports a warning (not a failure) when the file is missing.
- `e54c52f` New `cloche debug goroutines` and `cloche debug state` subcommands expose the running daemon's goroutine stacks, active run IDs, orchestration loops, and container session state; requires `cloched --debug-addr <addr>` or `CLOCHE_DEBUG=<addr>` or `[daemon] debug` in global config.

### Fixes

- `a5c63e3` When the outer workflow context is cancelled (e.g., via `cloche stop`), the engine now synthesizes a `fail` result for the active step and walks fail-branch wires (e.g., an `unclaim` step) before marking the run `cancelled`.
- `091673c` Broadcaster history is flushed to disk (as `full.log` and per-step logs) when a run is torn down, ensuring `cloche logs` returns output for runs that failed mid-execution with no on-disk log yet.
- `8152eef` Workflow-level `container { image = "..." }` is now read when dispatching a container sub-workflow via `workflow_name`, overriding the daemon default; previously the daemon default was always used regardless of the workflow's own container config.
- `ca58e67` `cloche shutdown --restart` now polls the daemon address until the old process stops accepting connections before launching the replacement, preventing two daemons from running simultaneously.
- `2185d67` `SessionFor` now has a dedicated 2-minute AgentReady timeout; when exceeded, the container is stopped and its logs are included in the returned error, replacing the previous behavior of blocking until the step's 30-minute timeout.
- `4d6f87f` After `docker start`, `runtime.Start` polls until the container reaches `Running` state and returns an error if it does not transition; a background goroutine also watches for early container exit so `SessionFor` fails fast with logs rather than waiting the full ready timeout.
- `46f0158` External directory and file symlinks in the project are now inlined as regular entries in the tar archive sent to the container, preventing Docker tarslip protection from silently dropping them and leaving the workspace incomplete.
- `7ff07bb` External symlinks nested inside an already-dereferenced external directory are now recursively inlined rather than emitted as symlink entries that Docker's tarslip guard would reject.
- `0d1d24b` `addDereferencedEntry` now returns an error when an external symlink's target is inaccessible (previously it printed a warning to stderr and silently continued, producing an incomplete workspace).
- `fdb3d32` `copyProjectToContainer` now closes the tar pipe with an error on walk failure (so `docker cp` receives a broken stream and exits non-zero) and treats any `docker cp` stderr output as an error even when the process exits 0.
- `f62b7bf` The daemon now rejects `EnableLoop` requests for project directories nested inside an already-active loop's scope, and stops superseded child loops when a parent loop is enabled; startup deduplication also filters nested paths.
- `02dee9e` Step log files are now opened in append mode in the generic and prompt adapters, and the session and host status handler track per-step byte offsets so only new output is written to `full.log` per loop iteration.

### UI/UX

- `8152eef` Adds `child_branch` to the auto-seeded KV store, set to the extracted git branch name before the container sub-workflow runs, so merge scripts can reference it without waiting for `child_run_id`.
- `e54c52f` `runtime.Start` now logs each sub-phase (create, copy project, copy auth, start, verify running) with wall-clock timing to aid diagnosis of slow or stuck container startup.

### Internal

- `ef9ad29` Added `cloched --project` flag to scope the daemon to a single project directory.
- `414760e` Reverts the `--project` flag added in `ef9ad29` (the approach was wrong; the correct fix is loop deduplication, implemented in `f62b7bf`).
- `2c3b541` Release publish script now unsets `GITHUB_TOKEN` before pushing to avoid using the environment token instead of the configured SSH key.

## v3.14.0 — 2026-04-15

### Breaking

- `3106e73` Removes wire output mapping syntax (`step:result -> next [ VAR = output.field ]`) from the DSL parser. Migration: remove `[ KEY = output.field ]` clauses from all wires in `.cloche/*.cloche` files.
- `0fe3c40` Removes `OutputMapping`, `OutputPath`, and `PathSegment` domain types and wire mapping evaluation from the host executor and docs. Migration: same as above.
- `58e52b7` Removes wire output mapping documentation from `docs/workflows.md` and `docs/USAGE.md` and cleans up residual executor code. Migration: same as above.
- `6398be0` Removes the `CLOCHE_STEP_OUTPUT` environment variable from host step scripts. Migration: print step output to stdout rather than writing to `$CLOCHE_STEP_OUTPUT`.
- `1444009` Removes the `feedback = "true"` step config key from the prompt adapter, domain types, and docs. Migration: remove `feedback = "true"` from step configs; use `{previous_output}` in prompt templates or read `$CLOCHE_PREV_OUTPUT` in script steps to access the preceding step's output.

### Features

- `416064d` Adds `changelog` and `release` host workflows to `.cloche/host.cloche` for automated changelog drafting, release tagging, and GitHub release publication. ([design](docs/plans/2026-04-15-release-process-design.md))
- `6bd5c8d` Adds `cloche extract <id>` CLI command to copy a container's `/workspace` to a git branch/worktree (`--at`, `--branch`) or plain directory (`--no-git --at`); the container must be retained with `--keep-container`. ([design](docs/plans/2026-04-14-cloche-extract-design.md))
- `d161d05` Adds `version` as an explicit subcommand to `cloche`, `cloched`, `cloche-agent`, and `clo`, alongside the existing `-v`/`--version` flags.
- `876b83c` Adds compound step name support to `cloche logs`: the form `subWorkflow:step` (e.g., `develop:implement`) addresses a specific step's log within a sub-workflow's extracted log directory; a 4-part composite ID (`task:attempt:subWorkflow:step`) is also accepted.
- `876b83c` `cloche init` now creates `prompts/`, `overrides/`, and `scripts/` subdirectories automatically during initialization.

### Fixes

- `876b83c` Container logs are now extracted from a sub-workflow's container using a background context when the parent context is cancelled (e.g., step timeout), so logs are preserved for post-mortem investigation.
- `29b1425` Removes the stray `protoc-25.1-linux-x86_64.zip` committed to the repository root and adds `protoc-*.zip` to `.gitignore` to prevent recurrence.

### UI/UX

- `a553e05` Improves `cloche extract` error messages: the error for a removed container now names the run ID and suggests `--keep-container`; the error for missing git data suggests `--no-git`.

### Internal

- `0601fea` Adds design document for `cloche extract`; initial refactor of `ExtractResults` to accept an `ExtractOptions` struct (preserving existing call-site behavior).
- `9c1bdf0` Extends `ExtractOptions` with `TargetDir`, `Branch`, `NoGit`, and `Persist` fields; introduces `dockerCp` package-level hook for test overriding; adds comprehensive unit tests.
- `07fd4a4` Adds `ExtractRun` gRPC RPC: defines `ExtractRunRequest`/`ExtractRunResponse` proto types, regenerates bindings, and implements the server handler.
- `465d10e` Adds table-driven `TestExtractResultsOptions` test suite covering all `ExtractOptions` field combinations.
- `5895efb` Removes duplicate `branchExists` helper introduced in `extract_test.go`.
- `bfa750c` Removes the `Env` map field from the `ExecuteStep` proto message (unused after wire mapping removal); updates generated code and documentation.
- `53989c0` Fixes the changelog collection script to retain develop-workflow squash commits in the commit corpus; updates the agent prompt to explain how to handle auto-generated commit subjects.

