# Design: `cloche init`

## Purpose

Scaffold a Cloche project in the current directory so a user can go from
zero to `cloche run` with minimal setup.

## CLI Interface

```
cloche init [--workflow <name>] [--image <base-image>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workflow` | `develop` | Workflow name; creates `<name>.cloche` |
| `--image` | `ubuntu:24.04` | Base Docker image for Dockerfile |

No flags required. Bare `cloche init` works with sensible defaults.

## Generated Files

```
.
├── <workflow>.cloche
├── Dockerfile
└── prompts/
    ├── implement.md
    └── fix.md
```

### Workflow (`develop.cloche`)

Generic 3-step: implement, test, fix loop. Placeholder `make test` command.

```
workflow "<name>" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> fix
  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
```

### Dockerfile

Multi-stage build: copies `cloche-agent` from the cloche builder image,
installs git and claude-code, creates `agent` user.

```dockerfile
FROM golang:1.25 AS cloche-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent

FROM <base-image>
RUN apt-get update && apt-get install -y git nodejs npm && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=cloche-builder /cloche-agent /usr/local/bin/cloche-agent
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace
USER agent
```

### Prompt Templates

**`prompts/implement.md`** — Generic implementation prompt with user request
injection point and basic guidelines.

**`prompts/fix.md`** — Generic fix prompt directing the agent to read
`.cloche/output/` logs and fix failures.

## Behavior

1. Refuse if `<workflow>.cloche` already exists (don't overwrite user work).
2. Create `prompts/` directory.
3. Write all template files.
4. Print next-steps message explaining what to customize.

## Implementation

New `init` subcommand added to `cmd/cloche/main.go`. Templates live as
string constants in `cmd/cloche/init.go` with `%s` substitution for
workflow name and base image. No template engine.

The `init` command is purely local — no daemon connection needed.
