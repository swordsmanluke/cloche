# Documentation Audit Report

## docs/INSTALL.md

- **Lines 17, 59 (`git clone` URLs) vs lines 47–50 (`go install` paths)**: The `git clone` URLs use `github.com/swordsmanluke/cloche` (matching the actual git remote), while all `go install` paths use `github.com/cloche-dev/cloche` (matching `go.mod` module path). These are inconsistent — either the repo needs to live under `cloche-dev` for `go install` to work, or `go.mod` and the install instructions need to switch to `swordsmanluke`. Currently `go install github.com/cloche-dev/cloche/...@latest` would fail because no such package exists on that path.

## docs/USAGE.md

- **Lines ~126–163 ("Repository Declarations" — `.cloche` file blocks)**: Documents top-level `repository "name" { path = "..." url = "..." }` blocks as valid DSL syntax that "are parsed independently from workflows." This feature does not exist. `internal/dsl/parser.go` `ParseAll` (lines 78–100) loops calling `parseWorkflow()`, which immediately calls `expectIdent("workflow")` (line 128). A file-level `repository` token produces a parse error. There is no `ParseRepositoriesFrom` function and no `parseRepository` method anywhere in the codebase. Any `.cloche` file containing such a block would fail to parse.

- **Line ~1740 (`make build` comment)**: Says `# Build cloche, cloched, cloche-agent to bin/` — omits `clo`. The Makefile `build` target (lines 6–10) builds all four binaries: `cloche`, `cloched`, `cloche-agent`, and `clo`. The comment should read "Build cloche, cloched, cloche-agent, clo to bin/".

## docs/workflows.md

Verified against source. No concrete inaccuracies found.

## docs/SAFETY.md

Verified against source. No concrete inaccuracies found.

## docs/web-dashboard.md

Verified against source. No concrete inaccuracies found.

## docs/agent-setup-claude.md

Verified against source. No concrete inaccuracies found.

## docs/agent-setup-codex.md

Verified against source. No concrete inaccuracies found.

## docs/built-in-agents.md

- **Line ~12 (`opencode` required args column)**: Lists required args as `_(none)_`. This is wrong. `internal/adapters/agents/prompt/prompt.go` lines 66–75 inject `--format json` whenever `ExplicitArgs` is set and `--format` is not already present — exactly the same required-arg pattern used for `claude`'s `--output-format stream-json`. The table should list `--format json` as a required arg for `opencode`.

## Summary

- **3 errors found across 3 files**: INSTALL.md (1), USAGE.md (2), built-in-agents.md (1).
- Most critical: USAGE.md documents a `repository` DSL block that does not exist — any user following this doc would write invalid `.cloche` files that fail to parse.
- INSTALL.md `go install` paths reference `github.com/cloche-dev/cloche` which doesn't match the actual git remote (`swordsmanluke`), making `go install` non-functional.
- built-in-agents.md incorrectly marks `opencode`'s `--format json` injection as non-existent, which could confuse users overriding `agent_args`.

## Undocumented Subsystems (Carried Forward)

### Undocumented Commands

No genuinely undocumented commands found. All subcommands registered in `cmd/cloche/main.go` (`run`, `resume`, `status`, `list`, `logs`, `poll`, `stop`, `delete`, `loop`, `shutdown`, `console`, `extract`, `version`, `init`, `health`, `get`, `set`, `tasks`, `activity`, `workflow`, `project`, `validate`, `doctor`, `debug`, `complete`) have at least a one-line description in `docs/USAGE.md`.

Note: `completionSubcommands` in `cmd/cloche/complete.go` omits `activity`, `console`, `doctor`, `extract`, and `version` from the tab-completion list — these commands won't be suggested by shell completion — but they are documented.

### Undocumented Subsystems

- `internal/adapters/local/` — Local subprocess runtime (enabled via `CLOCHE_RUNTIME=local`): the only documentation is a single parenthetical in the env var table ("subprocess, for dev only"). No design doc, no setup instructions, no description of behavior or limitations. `Attach` is unimplemented, `Logs` returns empty, and `Remove` is a no-op — constraints invisible to anyone trying to use this mode.

- `internal/logstream/` — Log streaming and broadcasting subsystem: provides the broadcaster pattern that fans gRPC `StepLog` events out to multiple subscribers and the log-line parser that formats tool-call blocks from agent output. No design or reference documentation exists. It underlies `cloche logs --follow` and live streaming in the web dashboard, but its architecture, broadcaster lifetime model, and parse format are undocumented.

### Undocumented DSL Features

- `usage_command` — Valid step config key. Documented in `docs/USAGE.md` (step configuration table) but absent from `docs/workflows.md`, which is the DSL reference.

- `prompt_step` — Valid step config key for `workflow_name` steps. Documented in `docs/USAGE.md` but absent from `docs/workflows.md`. The host-workflow design plan (`docs/plans/2026-03-06-host-workflow-design.md:63`) mentions it but the canonical DSL reference does not.
