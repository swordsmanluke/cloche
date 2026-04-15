# Cloche Changelog

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

