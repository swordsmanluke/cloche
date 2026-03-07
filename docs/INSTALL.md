# Installation

This document covers the various ways to install Cloche on your machine.

## Prerequisites

All installation methods require:

- **Docker** — Cloche runs workflows in containers
- **Git** — result extraction uses git worktrees
- **`ANTHROPIC_API_KEY`** — for agent steps using Claude Code

## Method Comparison

| Method | Requires Go | Auto Docker Image | Best For |
|--------|:-----------:|:-----------------:|----------|
| [Build from source](#build-from-source) | Yes | Yes | Contributors, full control |
| [`go install`](#go-install) | Yes | No | Go developers, quick setup |
| [Pre-built binaries](#pre-built-release-binaries-planned) | No | No | Most users (planned) |
| [Homebrew](#homebrew-macos--linux-planned) | No | No | macOS / Linux (planned) |
| [Copy binaries](#copy-binaries) | No | No | Air-gapped or ad-hoc deploys |
| [Docker-only](#docker-only-usage-planned) | No | N/A | CI, no host install (planned) |

All methods except Docker-only require building the `cloche-agent` Docker image
separately (unless the method does it automatically).

## Build from Source

Clone the repository, build the binaries and Docker image, and install them:

```
git clone https://github.com/cloche-dev/cloche.git
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
go install github.com/cloche-dev/cloche/cmd/cloche@latest
go install github.com/cloche-dev/cloche/cmd/cloched@latest
go install github.com/cloche-dev/cloche/cmd/cloche-agent@latest
```

This places the binaries in `$GOPATH/bin` (or `$HOME/go/bin` by default).

You still need to build the Docker image separately. Clone the repository and
run:

```
git clone https://github.com/cloche-dev/cloche.git
cd cloche
make docker-build
```

## Pre-built Release Binaries

Download pre-built binaries from the
[GitHub Releases](https://github.com/cloche-dev/cloche/releases) page. Each
release includes archives for common platforms (Linux amd64, Linux arm64, macOS
amd64, macOS arm64).

```
# Example: Linux amd64
curl -LO https://github.com/cloche-dev/cloche/releases/latest/download/cloche-linux-amd64.tar.gz
tar xzf cloche-linux-amd64.tar.gz
sudo install cloche cloched cloche-agent /usr/local/bin/
```

You still need to build the Docker image. Either clone the repo and run
`make docker-build`, or pull a pre-built image if one is published:

```
docker pull ghcr.io/cloche-dev/cloche-agent:latest
docker tag ghcr.io/cloche-dev/cloche-agent:latest cloche-agent:latest
```

## Homebrew (macOS / Linux)

If a Homebrew tap is published:

```
brew tap cloche-dev/tap
brew install cloche
```

This installs all three binaries. You still need the Docker image — the formula
will print post-install instructions for building or pulling it.

## Docker-Only Usage

You can run the Cloche daemon itself inside Docker, avoiding any host
installation beyond Docker. This is useful for CI environments or trying Cloche
without installing Go.

Build an all-in-one image from the repository:

```
git clone https://github.com/cloche-dev/cloche.git
cd cloche
docker build -t cloche-daemon -f Dockerfile.daemon .
```

Or run the daemon from the existing build image:

```
docker run -d \
  --name cloched \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /tmp/cloche.sock:/tmp/cloche.sock \
  -e ANTHROPIC_API_KEY \
  cloche-daemon
```

Note: Docker-in-Docker requires mounting the Docker socket. The CLI (`cloche`)
still needs to be available on the host (or in the same container) to
communicate with the daemon over the Unix socket.

### CI Integration

In a CI pipeline (GitHub Actions, GitLab CI, etc.), the Docker-only approach
avoids installing Go on runners. Until the daemon image is published, you can
build it as a CI step:

```yaml
# GitHub Actions example
- name: Build Cloche
  run: |
    git clone https://github.com/cloche-dev/cloche.git /tmp/cloche
    cd /tmp/cloche && make build && make docker-build
    sudo install /tmp/cloche/bin/* /usr/local/bin/
```

## Verifying the Installation

After installing, verify that everything works:

```
# Check the binaries
cloche --help
cloched --help

# Start the daemon (if not already running)
cloched &

# Build or pull the agent image
make docker-build   # or: docker pull ghcr.io/cloche-dev/cloche-agent:latest

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
