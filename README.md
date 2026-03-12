# CLOCHE

A system for grow-coding high quality applications.

# What is it

Cloche provides 
- containerized environments for coding agents to operate in isolation
- a workflow syntax for linking Agentic and Script-driven task nodes
- a focus on creating validated code
- self-evolving tooling that grows with your codebase in response to encountered errors

## Installation

See [docs/INSTALL.md](docs/INSTALL.md) for full installation instructions
covering build from source, `go install`, and other methods.

Quick version:

```
git clone https://github.com/cloche-dev/cloche.git
cd cloche
make install
```

This builds all binaries, builds the Docker image, installs to `~/.local/bin/`,
and starts the daemon. Prerequisites: Go 1.25+, Docker, Git, and an
`ANTHROPIC_API_KEY` for agent steps.

## Getting Started

### 1. Initialize your project

From your project's git repository:

```
cloche init
```

This scaffolds a `.cloche/` directory with a default `develop` workflow,
Dockerfile, prompt templates, host workflow, and configuration. All generated
files are safe to customize.

To use a different workflow name or base image:

```
cloche init --workflow my-workflow --base-image ubuntu:24.04
```

### 2. Configure the Dockerfile

Edit `.cloche/Dockerfile` to install your project's build dependencies. The
generated Dockerfile uses `cloche-base:latest` (which includes `cloche-agent`
and `git`). Add your language runtime, package manager, and any tools your
build and test steps need:

```dockerfile
FROM cloche-base:latest
RUN apt-get update && apt-get install -y ruby bundler
COPY . /workspace/
RUN cd /workspace && bundle install
```

The container image must have `cloche-agent` at `/usr/local/bin/cloche-agent`,
`git`, an `agent` user, and `/workspace` as the working directory. See
[docs/USAGE.md](docs/USAGE.md#dockerfile-requirements) for full requirements.

### 3. Define your workflow

Workflows live in `.cloche/*.cloche` files using the Cloche DSL. A workflow is
a directed graph of steps connected by result wiring. Steps are either agent
prompts or shell commands:

```
workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file(".cloche/prompts/fix.md")
    feedback = "true"
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail    -> abort
  test:success      -> done
  test:fail         -> fix
  fix:success       -> test
  fix:fail          -> abort
  fix:give-up       -> abort
}
```

Edit the prompt templates in `.cloche/prompts/` to describe the work your agent
should do. The `file()` function reads prompt content at execution time.

See [docs/workflows.md](docs/workflows.md) for the full DSL reference including
parallel branches, collect/join, wire output mappings, host workflows, and
container configuration.

### 4. Run a workflow

```
cloche run --workflow develop --prompt "Add a login page with email/password auth"
```

Cloche copies your project into a Docker container, runs the workflow steps, and
extracts results to a `cloche/<run-id>` git branch. Monitor progress with:

```
cloche status <run-id>
cloche logs <run-id> -f
```

### Let your LLM do the setup

Your LLM client of choice (Claude Code, Cursor, etc.) can likely handle the
full setup for you. Point it at this README and ask it to initialize Cloche for
your project — it can run `cloche init`, tailor the Dockerfile to your stack,
write prompt templates, and wire up a workflow suited to your build and test
pipeline.

### Further reading

- [docs/INSTALL.md](docs/INSTALL.md) — full installation options
- [docs/USAGE.md](docs/USAGE.md) — CLI reference, project layout, configuration
- [docs/workflows.md](docs/workflows.md) — workflow DSL syntax and semantics
- [examples/ruby-calculator/](examples/ruby-calculator/) — a complete project built by Cloche

## Containers

Coding agents are powerful, but running them interactively turns the human into the bottleneck.

On the otherhand, running them in "yolo" mode is wildly risky.

To allow our agents to run in a balance of safety and speed, we must disrupt the Lethal Trifecta.

Firstly, we remove their access to the full filesystem - the docker containers have access only to their
own filespace - only copies are used - no file mounts. Environment variables are only those provided by the host
to the container. 

Second, network access is limited to allowlists to ensure your agents can have e.g. free access to library documentation or your
internal documentation, but not the internet at large!


## Workflows

see our [Workflows](docs/workflows.md) documentation!

## Validated code

Your agents are fast, but that speed shouldn't come at the cost of quality.

Keep your validation checks in the loop with your coding agent - go beyond unit tests!
- validate complexity measurements
- automated code review
- auto-split large commits into stacked branches, individually reviewed and validated to keep commits simple and clean
- custom script and agent support to add your own checks to the mix
- failures are tracked and classified for use in autogenerating future validation checks
- validation failure feedback triggers retries to automatically resolve issues before they reach CI
