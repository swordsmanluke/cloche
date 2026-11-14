# Documentation Audit Report

Docs live in `repos/cloche/docs/`. Line numbers are approximate. Source paths are relative to `repos/cloche`.

## docs/USAGE.md
- **~L299, ~L1133**: Says `--prompt` is written to `.cloche/<run-id>/prompt.txt`. Source writes `.cloche/runs/<task-id>/prompt.txt` (`internal/adapters/docker/runtime.go:375-381`, `internal/adapters/grpc/executor.go:1009`).
- **~L1093-1111 (`cloche doctor`)**: Check list omits the intent store check (`checkIntentStore`, `cmd/cloche/doctor.go:121,483-492`).
- **~L501-512 (host step env table)**: Omits `CLOCHE_MODULE_DIR` (`internal/host/executor.go:248`) and `CLOCHE_RESULT_NONCE` (`executor.go:292`).
- **~L2008-2010**: Says zsh completion file is `~/.cloche/completions/_cloche`; code writes `cloche.zsh` (`cmd/cloche/completion_scripts.go:70`).
- **~L2001 (`cloche complete`)**: Says `logs`, `resume`, `delete` all suggest the last 20 runs. Only `resume`/`delete` do (`internal/adapters/grpc/server.go:2572-2577`, `2724-2726`). `logs` suggests task IDs and `task:attempt` IDs, falling back to runs only with no tasks (`server.go:2557-2567`, `2667`).
- **~L1741 (`cloche tasks`)**: Says it requires `CLOCHE_HTTP`. Code falls back to `localhost:8080` (`cmd/cloche/main.go:1505-1508`, `cmd/cloche/tasks_phase.go:170-172`). Doc itself says so at ~L2329.
- **~L1865-1868 (`cloche version`)**: Says agent version comes from the project's container image. Code uses `CLOCHE_IMAGE` or `cloche-agent:latest` (`cmd/cloche/main.go:1833-1855`).
- **~L2356 (`CLOCHE_AGENT_COMMAND`)**: Says the daemon injects it into the container. The Docker runtime does not set it (`internal/adapters/docker/runtime.go:150-181`); `cloche-agent` only reads it (`internal/agent/session.go:154`). `CLOCHE_PROJECT_DIR` is set per step by `cloche-agent` (`session.go:544,600`).
- **~L1557**: `cloche intent preview` usage omits `--repos` (`cmd/cloche/intent.go:595`).
- **~L1765**: `cloche signals token` also accepts `-p` (`cmd/cloche/signals.go:36`); undocumented.
- **~L2317 (borderline)**: `CLOCHE_HTTP` default `localhost:8080` comes only from the global config written by `cloche init`; the daemon's own fallback is empty (`cmd/cloched/main.go:215`).

## docs/workflows.md
- **~L254, ~L528**: Lists `CLOCHE_AGENT_COMMAND` in agent resolution order for all workflows. Host workflows never read it (`internal/host/executor.go:386-398`, `internal/host/runner.go:194-198`); only in-container `cloche-agent` does (`internal/agent/session.go:154`).
- **~L10**: Says container workflows use `agent`, `script`, or `workflow` steps. `poll` is also supported (`internal/agent/session.go:451`), as the doc's own Poll section says (~L723).
- **~L987 (`prev_step_exit`)**: Says it is an exit code. Source stores the result name, e.g. `success` (`internal/host/executor.go:143`, `internal/engine/engine.go:421,458`).
- **~L1000-1009 (`child_*` keys)**:
  - Only `child_run_id` is set after completion (`internal/adapters/grpc/executor.go:828`). The other keys are written when extract worktrees are prepared, before the run (`executor.go:~899-975`).
  - Bare `child_branch` is written only when exactly one worktree was prepared (`executor.go:973-974`).
  - `child_repos` holds extract repo names from the attempt's repo selection, written for single-repo runs too, not "repos the sub-workflow declared (multi-repo runs)".
- **~L973**: Says keys are seeded for every run. Host runs seed only when `TaskID != "" && Store != nil` (`internal/host/executor.go:112`); container-only runs get just `temp_file_dir` (`internal/adapters/grpc/server.go:858`).
- **~L818**: Poll script env strips `CLOCHE_RUN_ID`, `CLOCHE_TASK_ID`, and `CLOCHE_ATTEMPT_ID`, not only `CLOCHE_RUN_ID` (`internal/host/executor.go:~846-852`).
- **~L49-60 (task JSONL table)**: Omits `priority` and `labels` (`internal/host/task.go:23-30`).
- **~L72**: Names only `intent-scan` and `intent-scan-repo` as built-ins; `handle-notification` is also registered (`internal/builtin/builtin.go:16-20`).
- **~L148-150**: Says step `token-limit = 0` "aborts immediately". It yields a `token-limit` result routed by wiring (default `abort`); only a workflow-level 0 fails the run (`internal/engine/engine.go:223-232,285-289`).

## docs/modules.md
- Accurate against `internal/module/*`, `internal/domain/workflow.go`, `cmd/cloche/module.go`.
- Source note (not a doc error): host poll steps do not receive `CLOCHE_MODULE_DIR` (`internal/host/executor.go:~835`), though the doc says module `run` commands get it and the shipped `saga-judge` `await-answer` poll uses it. Likely a source bug.

## docs/built-in-agents.md
- **~L128**: Same `CLOCHE_AGENT_COMMAND` problem as workflows.md; not read for host workflows (`internal/agent/session.go:154`).
- Omission: `agent_command` may be a comma-separated fallback chain (`internal/adapters/agents/prompt/prompt.go:156`).

## docs/SAFETY.md
- **~L146-156**: Says a copy of the project, "not a bind mount". The run directory `.cloche/runs/<run-id>` is bind-mounted read-write, and `cfg.Volumes` mounts are added (`internal/adapters/docker/runtime.go:~190-200`). Only the project tree is copied.
- **~L139-141**: Says the base image runs as unprivileged `agent`. `docker/cloche-base/Dockerfile` has no `USER`; the runtime starts as root, chowns, then drops to `agent` via `gosu` (`runtime.go:~244-262`).

## docs/web-dashboard.md
- **~L32**: Says `cloche health` requires `CLOCHE_HTTP`. It also falls back to `[daemon] http` in the global config; it errors only if neither is set (`cmd/cloche/health.go:30-49`).

## docs/intent.md
- **~L460-468**: Says the console has no Intent panel (placeholder). Console has a Requirements button and `intent-drawer` (`internal/adapters/web/templates/console.html:21,109-114`); web-dashboard.md ~L346-351 documents it.
- **~L442-448**: `preview` synopsis omits `--repos` and the `-p` short form (`cmd/cloche/intent.go:575-595`).
- **~L229**: Implies doc globs are configurable. `IntentConfig` has no such key (`internal/config/config.go:115-121`); globs are hardcoded (`internal/intent/scan/collect.go:27-32`).
- Omission (~L316-325): post-task scan is also skipped when the daily budget cap is reached (`internal/adapters/grpc/server.go:~4095`).

## docs/INSTALL.md, docs/agent-setup-claude.md, docs/dras.md
- No inaccuracies found.

## docs/agent-setup-codex.md
- No inaccuracies found. A subagent claimed `[agents.codex] usage_command` is never read; that was wrong. It is applied for host steps when the command is codex (`internal/host/executor.go:403-404`). Not verified for in-container steps.

## Not audited
- `docs/CHANGELOG-DETAILED.md` (a changelog, not usage docs).
- Subagents did not exhaustively verify every table cell, e.g. the `cloche validate` check list and the DSL step-config validation rules in USAGE.md.

## Summary
- 32 findings across 7 files: USAGE.md 11, workflows.md 9, built-in-agents.md 2, SAFETY.md 2, web-dashboard.md 1, intent.md 4, plus the modules.md source note (not counted).
- Most were verified by a subagent only; I re-checked the codex claim and retracted it.
