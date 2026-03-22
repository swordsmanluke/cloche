# Installation

This document covers the various ways to install Cloche on your machine.

## Prerequisites

All installation methods require:

- **Docker** — Cloche runs workflows in containers
- **Git** — result extraction uses git worktrees

## Build from Source

Clone the repository, build the binaries and Docker image, and install them:

```
git clone https://github.com/swordsmanluke/cloche.git
cd cloche
make install
```

`make install` builds all three binaries (`cloche`, `cloched`, `cloche-agent`),
builds the `cloche-agent:latest` Docker image, installs the binaries to
`~/.local/bin/`, and starts the daemon. You can change the install prefix:

```
make install PREFIX=/usr/local
```

Make sure the install prefix is on your `PATH`:

```
export PATH="$HOME/.local/bin:$PATH"
```

### Build Requirements

- Go 1.25+

## `go install`

If you have Go installed, you can install the binaries directly:

```
go install github.com/swordsmanluke/cloche/cmd/cloche@latest
go install github.com/swordsmanluke/cloche/cmd/cloched@latest
go install github.com/swordsmanluke/cloche/cmd/cloche-agent@latest
```

This places the binaries in `$GOPATH/bin` (or `$HOME/go/bin` by default).

You still need to build the Docker image separately. Clone the repository and
run:

```
git clone https://github.com/swordsmanluke/cloche.git
cd cloche
make docker-build
```

## Pre-built Release Binaries (Planned)

> **Note:** Pre-built release binaries are not yet available. This section
> describes the planned installation method for when releases are published.

Download pre-built binaries from the
[GitHub Releases](https://github.com/swordsmanluke/cloche/releases) page. Each
release will include archives for common platforms (Linux amd64, Linux arm64,
macOS amd64, macOS arm64).

```
# Example: Linux amd64
curl -LO https://github.com/swordsmanluke/cloche/releases/latest/download/cloche-linux-amd64.tar.gz
tar xzf cloche-linux-amd64.tar.gz
sudo install cloche cloched cloche-agent /usr/local/bin/
```

You still need to build the Docker image. Clone the repo and run
`make docker-build`:

```
git clone https://github.com/swordsmanluke/cloche.git
cd cloche
make docker-build
```

## Homebrew (macOS / Linux) (Planned)

> **Note:** A Homebrew tap is not yet published. This section describes the
> planned installation method.

```
brew tap cloche-dev/tap
brew install cloche
```

This will install all three binaries. You will still need the Docker image —
the formula will print post-install instructions for building or pulling it.

## Verifying the Installation

After installing, verify that everything works:

```
# Check versions
cloche --version

# Start the daemon (if not already running)
cloched &

# Build the agent image
make docker-build

# Run a quick test from any git repository with a .cloche/ directory
cloche list
```

## Upgrading

To upgrade an existing installation, repeat your original installation method.
If you built from source:

```
cd cloche
git pull
make install
```

The `make install` target stops the running daemon, installs the new binaries,
and restarts the daemon automatically.

## Next Steps

- [Usage guide](USAGE.md) — quick start, CLI reference, and workflow setup
- [Workflow DSL reference](workflows.md) — full syntax for `.cloche` files
- [How to set up Claude Code](agent-setup-claude.md) — container setup for Claude
- [How to set up Codex](agent-setup-codex.md) — container setup for Codex
