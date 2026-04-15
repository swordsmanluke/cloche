# Cloche Detailed Changelog

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

