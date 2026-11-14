# Documentation Audit Report

## docs/USAGE.md
- **Line ~1121-1131**: `cloche run` synopsis/flag table omit `--repos a,b` (source: cmd/cloche/main.go:275, cmd/cloche/help.go:174). Only mentioned in passing at ~134.
- **Line ~1003-1007**: `cloche init` synopsis omits `--no-commit` (listed in table at ~1022) (source: cmd/cloche/init.go:1119).
- **Line ~1286-1292**: `cloche list` flag table omits `--issue, -i <filter>` (source: cmd/cloche/main.go:1017).
- **Line ~1544, ~1561**: `cloche intent preview` omits `--repos <list>` and the `-p` short form of `--prompt` (source: cmd/cloche/intent.go:585,595).
- **Line ~1914-1922**: `cloche threads list` omits `--no-color` (source: cmd/cloche/threads.go:55).
- **Line ~2040**: `active` default documented as `true`; config loader default is `false` (unset). `true` is only what `cloche init` writes (source: internal/config/config.go:203-229, cmd/cloche/init.go:165).
- **Line ~2017**: Layout lists `.cloche/activity.log`; activity is stored in the SQLite `activity` table (source: internal/adapters/sqlite/store.go:322, cmd/cloche/activity.go:77).
- **Line ~2304**: Client `CLOCHE_HTTP` default stated as `localhost:8080`; `cloche health` has no default and errors if unset, only `cloche tasks` falls back (source: cmd/cloche/health.go:35-51, cmd/cloche/main.go:1507).
- **Line ~103** (omission): `container {}` key list lacks `profile`, `dra`, `notes` (source: internal/dra/wake.go:114-117, internal/adapters/docker/locked.go:235).

## docs/workflows.md
- **Lines ~173, 213, 415, 465, 492, 509, 529, 563, 571, 603, 615, 860**: Examples use `workflow "name" {`; parser requires a bare identifier and rejects quoted names (source: internal/dsl/parser.go:493, internal/dsl/lexer.go:68-69).
- **Lines ~7-14 vs ~716**: Locations section says `poll` is host-only; Poll section says it works in both, and no restriction exists (source: internal/domain/workflow.go `ValidateLocation`, internal/dsl/parser.go:871-890).
- **Line ~382**: Says the parser checks wiring/orphans; that is `Workflow.Validate()` (source: internal/domain/workflow.go, internal/dsl/parser.go:581-645).
- **Line ~731**: Poll `results` implicit `timeout` only; every step also gets implicit `token-limit` (source: internal/dsl/parser.go:613-645).
- **Line ~982**: `prev_step_exit` described as exit code; it holds the previous step's result name (source: internal/host/executor.go:143, internal/engine/engine.go:421).
- **Line ~966**: Context keys said to be seeded for every run; only seeded when TaskID != "" and Store != nil (source: internal/host/executor.go:109-114).
- **Line ~1000**: `child_repos` described as repos the sub-workflow declared; it is the repos prepared/extracted for the run (source: internal/adapters/grpc/executor.go:899-950).

## docs/guide/
- **2-steps.md (~line 62)**: Agent example `step code` lacks opening `{`; won't parse (source: internal/dsl/parser.go:818).
- **2-steps.md (~line 34)**: Says all steps print `CLOCHE_RESULT:<step>:<result>` with nonce; nonce form is required only for agent steps, others use bare form (source: internal/host/executor.go:268-270,342; internal/adapters/agents/prompt/prompt.go:570-586). Also contradicts 8-patterns.md.
- **2-steps.md (~line 22)**: Says `success`/`fail` are implicit and exit 0 maps to success; only `timeout` and `token-limit` are implicit, and agent steps with no marker are `fail` (source: internal/dsl/parser.go:581-645).
- Other guide pages (README, 1, 3, 4, 5, 6, 7, 8): no verifiable errors.

## docs/web-dashboard.md
- **Line ~32-33**: Says `cloche health` requires `CLOCHE_HTTP`; it also falls back to `[daemon] http` in config (source: cmd/cloche/health.go:32-58).
- **Lines ~92-96**: `POST /trigger`, `POST /loop/stop`, `GET /loop/occupancy`, `GET /usage` shown as bare paths; real routes are project-scoped, e.g. `/api/projects/{name}/trigger` (source: internal/adapters/web/handler.go:376,393-398). Shorthand, minor.

## docs/intent.md
- **Line ~313-314**: Says `--full` re-mines only the one repo, not `[[repositories]]`; source mines project root plus all configured repos, and the doc contradicts itself at ~232-247 (source: cmd/cloche/intent.go:834-841,962-969; internal/intent/scan/collect.go:170-222).
- **Line ~448**: `cloche intent preview` synopsis omits `--repos <a,b>` (source: cmd/cloche/intent.go:595).
- **Line ~137**: "configured globs" implies a config key; none exists, only defaults (source: internal/config/config.go:115-121, internal/intent/scan/collect.go:26).

## docs/modules.md
- **Line ~183**: Lists `cloche module update <name>` only; real usage is `update <name> [<ref>] [--yes|-y]`, and all subcommands accept `--project/-p` (source: cmd/cloche/module.go:50-80,147-152).

## docs/INSTALL.md
- **Line ~22-23** (minor): Says `make install` builds `cloche-agent:latest`; it also builds `cloche-base:latest` and, if `saga` is on PATH, `cloche-dra:latest` (source: Makefile `docker-build`, `docker-dra`).

## docs/SAFETY.md, docs/agent-setup-claude.md, docs/agent-setup-codex.md, docs/built-in-agents.md, docs/dras.md
- Fully accurate against source for everything checked.

## Not audited
- docs/CHANGELOG-DETAILED.md and subdirectories docs/design, docs/plans, docs/init, docs/run-isolation.

## Summary
- 26 errors/mismatches found across 7 files (USAGE.md 9, workflows.md 7, guide 3, web-dashboard.md 2, intent.md 3, modules.md 1, INSTALL.md 1; several are omissions or minor shorthand).
