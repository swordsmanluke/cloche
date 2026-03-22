# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Cloche is a system for grow-coding high quality applications. It provides containerized
environments for coding agents, a workflow DSL for linking agentic and script-driven
tasks, validated code pipelines, and self-evolving tooling.

**Language:** Go

**Full design:** `docs/plans/2026-02-20-cloche-system-design.md`

## Architecture

Four binaries from one Go module:

- **`cloche`** (CLI) — Short-lived client. Talks to daemon over gRPC. Subcommands: `run`, `resume`, `status`, `list`, `logs`, `stop`, `project`.
- **`cloched`** (Daemon) — Long-running. Manages container lifecycle, collects status, persists state. Executes host workflows (any workflow with a `host { }` block) step by step on the host machine, dispatching container workflow runs as needed.
- **`cloche-agent`** (In-Container) — Autonomous. Parses workflow DSL, walks the graph, executes steps, streams status back to daemon. Runs to completion without human intervention.
- **`clo`** (In-Container CLI) — Lightweight gRPC client baked into container images. Provides `get`/`set`/`keys` commands for reading and writing the daemon's KV store from within a container step.

Hexagonal architecture: domain logic is independent of infrastructure. Ports define
interfaces (ContainerRuntime, RunStore, CaptureStore, AgentAdapter). Adapters implement
them (Docker, SQLite, gRPC, Claude Code).

```
cmd/
  cloche/           # CLI client
  cloched/          # Daemon
  cloche-agent/     # In-container entrypoint
  clo/              # In-container KV CLI
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
│   ├── host.cloche          # Host orchestration workflows (contain host { } blocks)
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

## Versioning

All four binaries share a single version string from `internal/version/VERSION`.
Format: `major.minor.build` (semver).

- **Build**: bugfixes, new dashboards, refactors, and everything that isn't a new major
  feature.
- **Minor**: new major features, and any change that would traditionally be a major
  version bump (backward-incompatible API changes, removing commands, etc.).
- **Major**: bumped only manually at the maintainer's direction. Multiple
  backward-incompatible changes may be batched into a single major release.

### Version commands

- `cloche -v` / `cloche --version` — prints CLI, daemon, and agent versions.
- `cloched -v` / `cloched --version` — prints daemon version.
- `cloche-agent -v` / `cloche-agent --version` — prints agent version.
- `clo -v` / `clo --version` — prints clo version.
- The daemon exposes a `GetVersion` gRPC endpoint.

### Versioning policy for bead tickets

When creating bead tickets, assess whether the change requires a minor version bump (new
major feature or backward-incompatible change). If so, tag the ticket so the version bump
is handled during the _finalize_ workflow while merging. All other changes only bump the
build number. Never bump the major version unless explicitly told to.
