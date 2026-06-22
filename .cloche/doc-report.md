# Documentation Audit Report

## docs/INSTALL.md

- **Lines ~18, ~46–48**: The `git clone` URL and all `go install` paths reference `github.com/swordsmanluke/cloche`, but `go.mod` declares the module as `github.com/cloche-dev/cloche`. `go install` uses the module path, not the VCS host, so `go install github.com/swordsmanluke/cloche/...@latest` would fail. The correct paths are `go install github.com/cloche-dev/cloche/cmd/cloche@latest`, etc.
  - Source: `/workspace/repos/cloche/go.mod` line 1: `module github.com/cloche-dev/cloche`

- **Line ~22**: Says "`make install` builds all three binaries (`cloche`, `cloched`, `cloche-agent`)". The `build` target in the Makefile actually builds **four** binaries — it also builds `clo` (`go build -o bin/clo ./cmd/clo`). The `install` target only installs three (intentionally — `clo` is an in-container binary), but the description should say four are built.
  - Source: `/workspace/repos/cloche/Makefile` `build` target

## docs/USAGE.md

- **Lines 26, 36, 103, 287, 417, 430, 565, 596, 619, 668, and every other DSL example**: Workflow names are shown unquoted throughout (e.g., `workflow develop {`, `workflow list-tasks {`, `workflow main {`). The parser's `parseWorkflow` calls `p.expect(TokenString)`, which requires a **quoted** string token. Unquoted identifiers (`TokenIdent`) produce a parse error at that position. Every real `.cloche` file in the repository uses quoted names (`workflow "develop" {`, `workflow "main" {`, etc.).
  - Source: `internal/dsl/parser.go:131`: `nameTok, err := p.expect(TokenString)`
  - Source: All `.cloche` files in the repo (e.g., `.cloche/host.cloche`, `.cloche/develop.cloche`, `examples/*/develop.cloche`)

- **Lines 920, 1650, 1666**: The default value of `CLOCHE_ADDR` is listed as `127.0.0.1:50051` in three separate places (doctor check description and both env var tables). The actual default returned by `config.DefaultAddr()` is `0.0.0.0:50051`, which binds all interfaces so in-container agents can reach the daemon via `host.docker.internal`.
  - Source: `internal/config/config.go:197–199`

- **Lines 127–145 (Repository Declarations section)**: Describes optional top-level `repository` blocks in `.cloche` files that are "parsed independently from workflows." No such parser exists. The DSL's `ParseAll` function iterates only `workflow` declarations and returns an error (`"no workflows found"`) for any file that contains none. There is no `parseRepository` function in the codebase. `FindAllWorkflows` silently skips files that fail to parse, so a `.cloche` file with only `repository` blocks would be silently ignored at runtime. Remote URLs for repositories can be configured directly in `config.toml` via the `url` key in `[[repositories]]` entries.
  - Source: `internal/dsl/parser.go:77–99` (`ParseAll` calls `p.parseWorkflow()` in a loop)
  - Source: `internal/host/runner.go:790–793` (parse errors cause the file to be silently skipped)
  - Source: `internal/config/config.go:63–66` (`RepositoryConfig` has a `url` toml field)

- **Line 1711**: `make build # Build cloche, cloched, cloche-agent to bin/` — omits `clo` from the list of binaries built.
  - Source: `/workspace/repos/cloche/Makefile` `build` target: also runs `go build -o bin/clo ./cmd/clo`

- **Lines 1313–1314 (`clo` section)**: "clo reads `CLOCHE_ADDR`, `CLOCHE_TASK_ID`, and `CLOCHE_ATTEMPT_ID` from the environment." The `clo` binary also reads `CLOCHE_RUN_ID` and passes it to KV scope resolution (used for per-run scope lookups).
  - Source: `cmd/clo/main.go`: `runID := os.Getenv("CLOCHE_RUN_ID")`

## docs/workflows.md

- **Lines 103–134 (DSL Syntax section) and all other examples in the file**: Same as USAGE.md — workflow names are shown unquoted (e.g., `workflow develop {`). The parser requires quoted strings.
  - Source: same as USAGE.md finding above

- **Lines 267–297 (Repository Declarations section)**: States repository blocks in `.cloche` files work if they are in separate files from `workflow` blocks. The constraint is documented, but the underlying premise — that a file with only `repository` blocks can be parsed at all — is incorrect. `ParseAll` requires at least one `workflow` block per file and errors otherwise. There is no parser support for `repository` blocks in `.cloche` files.
  - Source: `internal/dsl/parser.go:96–98`: returns `"no workflows found"` for files with no `workflow` blocks

## docs/SAFETY.md

Verified against source. No concrete inaccuracies found.

## docs/web-dashboard.md

Verified against source. No concrete inaccuracies found.

## docs/agent-setup-claude.md

Verified against source. No concrete inaccuracies found.

## docs/agent-setup-codex.md

Verified against source. The workflow example correctly uses `workflow "develop" {` with quotes — this file is accurate.

---

## Summary

9 errors found across 3 files:

| # | File | Issue |
|---|------|-------|
| 1 | INSTALL.md | Wrong GitHub org and module path in clone/install commands (`swordsmanluke` → `cloche-dev`) |
| 2 | INSTALL.md | `make install` description says "three binaries"; build produces four (including `clo`) |
| 3 | USAGE.md | All DSL examples show unquoted workflow names; parser requires quoted strings |
| 4 | USAGE.md | Default `CLOCHE_ADDR` listed as `127.0.0.1:50051`; actual default is `0.0.0.0:50051` |
| 5 | USAGE.md | `repository` blocks in `.cloche` files described as parseable; no parser support exists |
| 6 | USAGE.md | `make build` description omits `clo` binary |
| 7 | USAGE.md | `clo` env var list omits `CLOCHE_RUN_ID` |
| 8 | workflows.md | All DSL examples show unquoted workflow names (same as USAGE.md) |
| 9 | workflows.md | `repository` blocks in separate `.cloche` files implied to work; no parser support exists |

---

## Missing Documentation

### Undocumented Commands

All registered `cloche` subcommands (`run`, `resume`, `status`, `list`, `logs`, `poll`, `stop`, `delete`, `extract`, `loop`, `shutdown`, `console`, `init`, `doctor`, `health`, `workflow`, `validate`, `project`, `get`, `set`, `tasks`, `activity`, `version`, `debug`, `complete`) have at least a one-line description in `docs/USAGE.md`. No commands are missing from usage documentation.

### Undocumented Subsystems

- `internal/activitylog/` — no design or reference documentation. The `cloche activity` CLI command is documented in `docs/USAGE.md`, but the package's internals — event kinds (`KindAttemptStarted`, `KindAttemptEnded`, `KindStepStarted`, `KindStepEnded`), the `ActivityStore` interface, and its SQLite backing — are not described anywhere in `docs/`.

- `internal/projectcli/` — no design or reference documentation. This package provides rendering helpers for `cloche project repos list` output (`WriteReposList`). The command behaviour is documented in `docs/USAGE.md` but the package is never mentioned as an architectural component in any design or reference doc.

### Undocumented DSL Features

All DSL keywords, block types, step fields, and template directives supported by the parser are covered in `docs/workflows.md`:

- Block types: `workflow`, `agent`, `step`, `collect`, `host`, `container` — all documented.
- Step fields: `prompt`, `run`, `workflow_name`, `poll`, `interval`, `timeout`, `max_attempts`, `agent`, `agent_command`, `agent_args`, `usage_command`, `prompt_step`, `results` — all documented.
- Wiring syntax: `->` arrow, `result:` colon notation, `all`/`any` collect conditions — all documented.
- Template directives: `{{! cmd }}`, `{{@ path }}`, `$$` — all documented.
- Built-in result terminals: `done`, `abort`, `timeout`, `give-up` convention — all documented.

No DSL features are missing from `docs/workflows.md`.
