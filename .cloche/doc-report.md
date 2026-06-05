# Documentation Audit Report

Generated: 2026-06-05

---

## docs/INSTALL.md

- **Lines 17, 45–48, 57, 74, 83** (`go install` / `git clone` paths): All five code blocks use
  `github.com/swordsmanluke/cloche` as the repository / module path. The actual Go module
  declared in `go.mod:1` is `github.com/cloche-dev/cloche`. The `go install` commands
  (`go install github.com/swordsmanluke/cloche/cmd/cloche@latest`, etc.) would resolve the
  wrong module and fail. Source: `/workspace/repos/cloche/go.mod:1`.

- **Line 38** (Go version requirement): States "Go 1.25+". The `go.mod` directive is
  `go 1.25.3`, requiring at least that patch release. "Go 1.25+" is imprecise — a toolchain
  at 1.25.0 or 1.25.2 would fail. Source: `/workspace/repos/cloche/go.mod:3`.

---

## docs/USAGE.md

- **Lines 129–163 (Repository Declarations — `.cloche` files subsection)**: Claims that
  `.cloche` files support top-level `repository "name" { path = "..." url = "..." }` blocks,
  "parsed independently from workflows." The DSL parser (`internal/dsl/parser.go:125–130`)
  only recognises `workflow` as a top-level keyword; encountering `repository` produces a
  parse error (`expected "workflow", got "repository"`). No `ParseRepository` or equivalent
  function exists anywhere in the codebase. These blocks are not parsed from `.cloche` files
  at all. Source: `internal/dsl/parser.go:77–99, 125–130`.

- **Lines 1619–1635 (`[[repositories]]` table)**: The table lists only `name` (required) and
  `path` (required). However, `internal/config/config.go:65` defines a `URL string
  toml:"url"` field on `RepositoryConfig`, which is loaded and stored in
  `internal/project/loader.go:29`. The `url` key is valid in `config.toml`
  `[[repositories]]` entries but undocumented in this table. Source:
  `internal/config/config.go:59–66`.

- **Lines 1557–1563 (`[orchestration]` config table)**: Documents `dedup_seconds` as having
  a default of `300`. The `defaults()` function in `internal/config/config.go:78–95` does
  not initialise `DedupSeconds`; the Go zero value (`0.0`) is used when the field is absent
  from `config.toml`. The runtime consequence is that deduplication is disabled (timeout = 0)
  unless the user explicitly sets the key. The intent is documented as a comment on the struct
  field (`// default: 300`) and as a commented-out example in the generated config template
  (`cmd/cloche/init.go:135`: `# dedup_seconds = 300.0`), but neither constitutes an enforced
  default. Source: `internal/config/config.go:35, 78–95`; `internal/adapters/grpc/server.go:3123–3130`.

- **Line 1713 (`make build` build command)**: Documented as "Build cloche, cloched,
  cloche-agent to bin/". The actual `Makefile:6–10` builds four binaries: `cloche`, `cloched`,
  `cloche-agent`, and `clo`. The `clo` binary is omitted from the description. Source:
  `Makefile:10`.

---

## docs/workflows.md

- **Lines 289–319 (Repository Declarations)**: States that `.cloche` files support top-level
  `repository { }` blocks (in files that do not also contain `workflow` blocks). This is
  incorrect. The DSL parser's `ParseAll` function (`internal/dsl/parser.go:77–99`) iterates
  by calling `parseWorkflow()` in a loop; `parseWorkflow()` starts with
  `expectIdent("workflow")` (`parser.go:125–130`), which returns a parse error for any
  token other than `workflow`. A `.cloche` file whose first top-level declaration is
  `repository` would fail to parse. Additionally, `ParseAll` returns an error if no
  workflows are found, so a repository-only file is also invalid. Source:
  `internal/dsl/parser.go:77–99, 125–130`.

---

## docs/SAFETY.md

No concrete inaccuracies found. Claims about container isolation, auth file copying, and
network allowlisting are consistent with the source.

---

## docs/web-dashboard.md

- **Lines 33–34**: States "The CLI commands `cloche tasks` and `cloche health` also require
  `CLOCHE_HTTP` to be set." This is accurate for `cloche health` (which calls
  `resolveHTTPAddr()` and exits with an error if the address is empty), but inaccurate for
  `cloche tasks`, which falls back to `localhost:8080` when `CLOCHE_HTTP` is unset
  (`cmd/cloche/main.go:1148–1149`). Source: `cmd/cloche/main.go:1148–1149`;
  `cmd/cloche/health.go:41–47`.

---

## docs/agent-setup-claude.md

No concrete inaccuracies found.

---

## docs/agent-setup-codex.md

No concrete inaccuracies found.

---

## Summary

**7 errors found across 4 files.**

| # | File | Severity | Issue |
|---|------|----------|-------|
| 1 | INSTALL.md | High | `go install` / `git clone` use `swordsmanluke/cloche` but module is `cloche-dev/cloche` |
| 2 | INSTALL.md | Low | Go version requirement states "Go 1.25+" but go.mod requires `go 1.25.3` |
| 3 | USAGE.md | High | `.cloche` file `repository {}` blocks claimed to be parsed; they are not supported by the DSL |
| 4 | USAGE.md | Medium | `[[repositories]]` table omits the valid `url` key |
| 5 | USAGE.md | Medium | `dedup_seconds` documented default `300` is not enforced; Go default is `0.0` |
| 6 | USAGE.md | Low | `make build` description omits `clo` binary, which is built by the Makefile |
| 7 | web-dashboard.md | Low | `cloche tasks` described as requiring `CLOCHE_HTTP`; it actually falls back to `localhost:8080` |
