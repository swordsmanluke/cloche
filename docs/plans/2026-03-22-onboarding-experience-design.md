# Onboarding Experience Design

**Date:** 2026-03-22
**Status:** Design

## Problem

Multiple users have spent hours trying to get Cloche set up and running on their
projects. The friction comes from two directions: (1) the scaffolded workflow and
Dockerfile require manual editing before anything works, with no guidance on what
to change for a given project, and (2) when something goes wrong during setup —
a container fails to build, the agent can't authenticate, the daemon isn't
reachable — there's no unified diagnostic tool to identify the problem.

## Solution

Two changes address the onboarding gap:

**LLM-assisted `cloche init`** — Instead of generating static templates that
require manual editing, init generates template files with clear placeholders,
then invokes the user's configured LLM client to analyze the project and fill
them in. The LLM examines the repo to determine how tests are run, what
dependencies the container needs, and what the project structure looks like, then
populates the workflow, Dockerfile, and test script accordingly. The result is a
project that can run its first task with zero manual edits.

**`cloche doctor`** — A diagnostic command that checks every layer of the setup
stack in order: Docker availability, base image existence, daemon reachability,
project configuration, and finally an end-to-end smoke test that sends a simple
prompt through the containerized agent and verifies it completes. This replaces
the current debugging approach of "try it and read container logs."

## Design Details

### LLM-Assisted Init

#### Flow

`cloche init` retains its current behavior as the baseline: it scaffolds
directories and writes template files. The new behavior is an additional phase
that runs after scaffolding:

1. **Scaffold** — Create `.cloche/` structure exactly as today, but with
   explicit placeholder markers in generated files (see Template Format below).
2. **Analyze** — If an LLM client is available, invoke it with the project
   context to fill in placeholders. If no LLM is available, skip this phase and
   leave placeholders for the user to fill manually.
3. **Report** — Print what was generated and what still needs manual attention.

#### LLM Client Configuration

The LLM client used during init is configured via:

1. `--agent-command <cmd>` flag on `cloche init`
2. `CLOCHE_AGENT_COMMAND` environment variable
3. Global config `~/.config/cloche/config` under `[daemon]` `llm_command`
4. Fall back to `claude` if available on PATH

The client is invoked as a subprocess with a structured prompt on stdin. It does
not need to be the same agent used for workflow steps — it just needs to accept a
prompt and produce text output.

#### Template Format

Template files use clear placeholder blocks that both humans and the LLM can
understand:

```
# In develop.cloche, the test step:
step test {
    run     = "# TODO(cloche-init): Replace with your project's test command"
    results = [success, fail]
}
```

```dockerfile
# In Dockerfile:
FROM cloche-agent:latest
USER root

# TODO(cloche-init): Install your project's dependencies
# Examples:
#   RUN apt-get update && apt-get install -y python3 pip
#   RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

USER agent
```

The `TODO(cloche-init)` marker serves double duty: the LLM knows to replace
these blocks, and if the LLM phase is skipped, users can grep for them to find
what needs attention.

#### LLM Prompt Design

The init LLM prompt includes:

- A file listing of the project root (filtered through `.gitignore`)
- Contents of key project files (`package.json`, `go.mod`, `Cargo.toml`,
  `pyproject.toml`, `Makefile`, `Gemfile`, etc. — whichever exist)
- The template files with placeholders
- Instructions: "Fill in the TODO(cloche-init) placeholders based on this
  project. Output each file as a fenced block with the file path as the info
  string."

The output is parsed and written back to the template files, replacing only the
placeholder blocks. If the LLM output can't be parsed or the LLM fails, the
original templates are left intact and a warning is printed.

#### What Gets Filled In

| File | Placeholder | LLM determines |
|---|---|---|
| `develop.cloche` | test step `run` command | How tests are run in this project |
| `Dockerfile` | dependency installation | What packages/runtimes the project needs |
| `prompts/implement.md` | project-specific guidelines | Coding conventions, framework notes |

The workflow structure (steps, wiring, transitions) is not modified by the LLM —
only the `run` commands and prompt content within steps.

#### Skipping LLM Phase

`cloche init --no-llm` skips the analysis phase entirely. This keeps init
scriptable and deterministic for CI or automation use cases.

### `cloche doctor`

#### Command Interface

```
cloche doctor [--project <dir>] [--verbose]
```

Runs in the current directory by default. Checks are run in dependency order;
each check prints a status line. A failing check that blocks subsequent checks
is marked as fatal and stops the sequence.

#### Check Sequence

```
$ cloche doctor

Checking Docker...                     ok
Checking base image (cloche-base)...   ok
Checking daemon (127.0.0.1:50051)...   ok
Checking project config...             ok
Checking workflow syntax...            ok
Checking project image build...        ok (built myproject-cloche-agent:latest)
Checking agent roundtrip...            ok (agent responded in 12s)

All checks passed.
```

#### Check Details

**1. Docker daemon reachable**

Run `docker info` and check exit code. On failure, print platform-specific
guidance ("Is Docker Desktop running?" on macOS, "Is the docker service
started?" on Linux).

Implementation: `cmd/cloche/doctor.go`, function `checkDocker()`.

**2. Base image exists**

Check whether `cloche-base:latest` (or `cloche-agent:latest` for older setups)
exists via `docker image inspect`. On failure, print instructions for building
it (`make docker-base` or download instructions).

Implementation: `checkBaseImage()`.

**3. Daemon reachable**

Attempt a gRPC connection to the daemon address (from config or default
`127.0.0.1:50051`). Call `GetVersion` to verify it responds. On failure,
suggest `cloche shutdown --restart` or check if another process holds the port.

Implementation: `checkDaemon()`. Reuses the existing client connection logic
from `cmd/cloche/main.go`.

**4. Agent authentication**

Check that authentication credentials exist for the configured agent. For
Claude Code: check `~/.claude/` directory exists and contains session data, or
`ANTHROPIC_API_KEY` is set. For other agents: check the relevant env var or
config. Print which auth method was detected.

This is a soft check (warning, not fatal) since some agents may have other auth
mechanisms.

Implementation: `checkAuth()`.

**5. Project configuration** (only if in a cloche project)

Load `.cloche/config.toml` and report any parse errors. Warn if `active` is
still `false`. Check that the configured `image` name is valid.

Check for remaining `TODO(cloche-init)` markers in workflow and Dockerfile —
warn if found.

Implementation: `checkProjectConfig()`. Reuses `config.Load()`.

**6. Workflow validation** (only if in a cloche project)

Run the same checks as `cloche validate` — parse all `.cloche/*.cloche` files,
verify wiring, check file references. Report errors inline rather than as a
separate command.

Implementation: `checkWorkflows()`. Reuses `validateProject()` from
`cmd/cloche/validate.go`.

**7. Project image build** (only if in a cloche project)

Trigger `EnsureImage` for the project's configured image. This builds the
image if needed or confirms it's up-to-date. On failure, print the Docker
build output so the user can see what went wrong.

Implementation: `checkImageBuild()`. Calls the Docker adapter's `EnsureImage`
directly (or via a gRPC call to the daemon).

**8. Agent roundtrip** (only if in a cloche project)

The definitive end-to-end check. Start a container from the project image,
run a minimal agent prompt ("Create a file called /tmp/doctor-test containing
'ok'"), and verify the file exists in the container. This tests:

- Container starts successfully
- Agent binary works inside the container
- Agent can receive and execute prompts
- Network/auth works (if the agent needs API access)

On failure, capture and display container logs. On timeout (configurable,
default 60s), kill the container and report.

Implementation: `checkAgentRoundtrip()`. Uses the Docker adapter to start a
short-lived container with a test workflow.

#### Output Modes

- Default: one status line per check, details on failure
- `--verbose`: print details for all checks, including timing and config values
- Exit code 0 if all checks pass, 1 if any check fails

### Error Handling

**LLM init phase failures** are never fatal to `cloche init`. If the LLM
subprocess fails, times out (30s default), or produces unparseable output, the
template files are left with their placeholder markers. A warning is printed
explaining what happened and that the user should fill in placeholders manually.

**Doctor check failures** print actionable guidance specific to the failure
mode. Each check function returns a `CheckResult` with status, message, and
optional remediation steps. The doctor command formats these consistently.

**Doctor roundtrip timeout** defaults to 60 seconds. Override with
`--timeout <duration>`. The container is always cleaned up, even on timeout.

## Alternatives Considered

**Project type detection without LLM** — We considered hardcoding detection
logic (if `go.mod` exists, use `go test ./...`; if `package.json`, use
`npm test`). This handles common cases but breaks down for projects with
non-standard test setups, monorepos, or multiple languages. The LLM approach
handles arbitrary projects and produces better Dockerfile content. The
`TODO(cloche-init)` placeholder approach still works when no LLM is available,
so this is strictly additive.

**Interactive init wizard** — A step-by-step interactive setup was considered
but rejected to keep `cloche init` scriptable. The LLM phase achieves the same
goal (less manual work) without requiring user interaction.

**Auto-starting the daemon from `cloche` commands** — Considered having every
`cloche` command auto-start the daemon if it's not running. Rejected due to the
risk of double-launching daemons and the confusion that creates. `cloche doctor`
tells you the daemon isn't running; `cloche shutdown --restart` is the
recommended fix.

**Merging doctor into validate** — `cloche validate` already checks workflow
syntax. We could extend it to check infrastructure too. Rejected because
validate is about project correctness (are my files right?) while doctor is
about environment readiness (is my machine set up?). They have different
audiences and failure modes.

## Implementation Notes

### File Layout

```
cmd/cloche/
  doctor.go          # cloche doctor command + all check functions
  init.go            # Modified: add LLM analysis phase + placeholder templates
  init_llm.go        # LLM invocation, prompt construction, output parsing
```

### Sequencing

Recommended implementation order:

1. **`cloche doctor`** — Immediately useful, no changes to existing behavior.
   The roundtrip check alone will save hours of debugging.
2. **Template placeholders** — Update init templates to use `TODO(cloche-init)`
   markers. Low risk, improves the no-LLM path too.
3. **LLM-assisted init** — Add the analysis phase. Can be iterated on
   independently since failures are non-fatal.
