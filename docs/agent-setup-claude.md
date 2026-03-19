# How to Set Up Claude Code

This guide covers configuring Cloche containers to use
[Claude Code](https://docs.anthropic.com/en/docs/claude-code) as the coding agent.

## Prerequisites

- A working Cloche installation ([Installation guide](INSTALL.md))
- Claude Code installed on the host (`npm install -g @anthropic-ai/claude-code`)
- An active Claude Code session on the host (run `claude` once to authenticate)

## How Authentication Works

Cloche automatically copies your host's `~/.claude/` directory and `~/.claude.json`
file into each container at `/home/agent/.claude` and `/home/agent/.claude.json`. This
reuses your existing Claude Code session so containers authenticate without needing an
API key.

The files are copied (not bind-mounted) so each container gets its own copy, avoiding
concurrent write conflicts when multiple runs execute in parallel.

## Dockerfile

Your `.cloche/Dockerfile` must install Claude Code via npm:

```dockerfile
FROM cloche-base:latest
USER root

# Install Node.js (required for Claude Code)
RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Add your project's build dependencies here
# RUN apt-get install -y ...

USER agent
```

## Workflow Configuration

Claude is the default agent, so no explicit configuration is needed. These are
equivalent:

```
// Implicit — claude is the default
step implement {
  prompt = file(".cloche/prompts/implement.md")
  results = [success, fail]
}

// Explicit
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_command = "claude"
  results = [success, fail]
}
```

Default arguments passed to Claude Code:

```
-p --output-format stream-json --verbose --dangerously-skip-permissions
```

Override with `agent_args` if needed:

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_args = "-p --verbose --dangerously-skip-permissions"
  results = [success, fail]
}
```

## Using an API Key Instead

If you prefer to use an API key rather than session reuse, set `ANTHROPIC_API_KEY` in
the daemon's environment. Cloche passes it into containers automatically:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
cloched &
```

This works alongside session reuse — Claude Code will use the API key if no session
is available.

## Verifying the Setup

Run a quick test workflow:

```bash
cloche run develop --prompt "Print hello world"
```

Check logs to confirm Claude Code is executing:

```bash
cloche logs <run-id>
```

## Troubleshooting

**Claude Code not found in container**: Make sure your Dockerfile installs
`@anthropic-ai/claude-code` via npm and that Node.js is available.

**Authentication errors**: Run `claude` on the host to ensure your session is active.
Cloche copies `~/.claude/` at container creation time, so a stale or missing session
on the host means containers won't authenticate.

**Permission errors on `~/.claude`**: Cloche runs `chown -R agent:agent` on the copied
auth files at container startup. If you see permission errors, check that your
Dockerfile does not override the entrypoint.
