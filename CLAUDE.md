# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Cloche is a system for grow-coding high quality applications. It provides containerized
environments for coding agents, a workflow DSL for linking agentic and script-driven
tasks, validated code pipelines, and self-evolving tooling.

**Language:** Go

**Full design:** `docs/plans/2026-02-20-cloche-system-design.md`

## Architecture

Three binaries from one Go module:

- **`cloche`** (CLI) — Short-lived client. Talks to daemon over gRPC. Subcommands: `run`, `status`, `list`, `logs`, `stop`.
- **`cloched`** (Daemon) — Long-running. Manages container lifecycle, collects status, persists state. Executes host workflows (`host.cloche`) step by step on the host machine, dispatching container workflow runs as needed.
- **`cloche-agent`** (In-Container) — Autonomous. Parses workflow DSL, walks the graph, executes steps, streams status back to daemon. Runs to completion without human intervention.

Hexagonal architecture: domain logic is independent of infrastructure. Ports define
interfaces (ContainerRuntime, RunStore, CaptureStore, AgentAdapter). Adapters implement
them (Docker, SQLite, gRPC, Claude Code).

```
cmd/
  cloche/           # CLI client
  cloched/          # Daemon
  cloche-agent/     # In-container entrypoint
internal/
  domain/           # Core types (Workflow, Step, Result, Run)
  ports/            # Interfaces (store, container, agent, notifier)
  adapters/         # Implementations (docker, sqlite, grpc, agents)
  host/             # Host workflow executor (runs steps on host machine)
```

## Workflow DSL

Steps report named results; wiring maps results to next steps. `done` and `abort` are
built-in terminals. See `docs/workflows.md` for syntax and semantics.

## Project Layout

Cloche-specific files live in `.cloche/` at the project root:

```
my-project/
├── .cloche/
│   ├── develop.cloche      # Container workflow definition
│   ├── host.cloche          # Host orchestration workflow (runs on host)
│   ├── Dockerfile           # Container image
│   ├── prompts/             # Prompt templates for agent steps
│   ├── scripts/             # Host-side scripts (e.g. prepare-prompt.sh)
│   ├── overrides/           # Files copied on top of /workspace/ in container
│   └── <run-id>/            # Runtime state (gitignored)
├── src/                     # Existing project source (untouched)
└── CLAUDE.md
```

## Container Model

One container per workflow run (not per step). The entire project root is copied into the
container at `/workspace/`, then override files from `.cloche/overrides/` are applied on
top. Results are extracted to git branches. Network is allowlisted.

## Agent Support

Agent-agnostic. The `AgentAdapter` interface wraps any coding agent. Initial adapters:
Generic (arbitrary commands) and Claude Code.
