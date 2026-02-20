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

- **`cloche`** (CLI) — Short-lived client. Talks to daemon over gRPC. Subcommands: `run`, `status`, `logs`, `stop`.
- **`cloched`** (Daemon) — Long-running. Manages container lifecycle, collects status, persists state. Does NOT interpret workflow logic.
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
```

## Workflow DSL

Steps report named results; wiring maps results to next steps. `done` and `abort` are
built-in terminals. See `docs/workflows.md` for syntax and semantics.

## Container Model

One container per workflow run (not per step). Files are copied in; git push extracts
results to host on agent-specific branches. Network is allowlisted. Env vars are the
image's responsibility.

## Agent Support

Agent-agnostic. The `AgentAdapter` interface wraps any coding agent. Initial adapters:
Generic (arbitrary commands) and Claude Code.
