# How to Set Up Codex

This guide covers configuring Cloche containers to use
[OpenAI Codex](https://platform.openai.com/docs) as the coding agent.

## Prerequisites

- A working Cloche installation ([Installation guide](INSTALL.md))
- An OpenAI API key with Codex access

## How Authentication Works

Unlike Claude Code, Codex authenticates via an API key rather than a local session.
You pass your OpenAI API key into containers using environment variables.

## Dockerfile

Your `.cloche/Dockerfile` must install the Codex CLI:

```dockerfile
FROM cloche-base:latest
USER root

# Install Node.js (required for Codex CLI)
RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# Install Codex CLI
RUN npm install -g @openai/codex

# Add your project's build dependencies here
# RUN apt-get install -y ...

USER agent
```

## Passing the API Key

Set `OPENAI_API_KEY` using `CLOCHE_EXTRA_ENV` in the daemon's environment:

```bash
export CLOCHE_EXTRA_ENV="OPENAI_API_KEY=sk-..."
cloched &
```

`CLOCHE_EXTRA_ENV` accepts comma-separated `KEY=VALUE` pairs and injects them into
every container.

## Workflow Configuration

Set `agent_command` to `codex` at the workflow or step level:

**Workflow level** (applies to all steps):

```
workflow "develop" {
  container {
    agent_command = "codex"
  }

  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}
```

**Step level** (per-step override):

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_command = "codex"
  results = [success, fail]
}
```

Codex is not a "known" agent in Cloche, so it receives the prompt on stdin with no
default arguments. Add `agent_args` if the Codex CLI needs specific flags:

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_command = "codex"
  agent_args = "--full-auto"
  results = [success, fail]
}
```

## Fallback Chains

Use Codex as a fallback if Claude is unavailable:

```
agent_command = "claude,codex"
```

Or use Codex as the primary with Claude as fallback:

```
agent_command = "codex,claude"
```

See [Agent Command Resolution](USAGE.md#agent-command-resolution) for full fallback
semantics.

## Token Usage Capture

Codex does not emit token usage in its output stream. Cloche can capture usage by
running a shell command after each agent step and parsing its JSON output.

### Via `config.toml` (recommended for all Codex steps)

Add an `[agents.codex]` section to `.cloche/config.toml`. The command is run
automatically whenever the active agent command is `codex`:

```toml
[agents.codex]
usage_command = "codex usage --last --json"
```

The command must print JSON to stdout:

```json
{"input_tokens": 1234, "output_tokens": 567}
```

If the command fails or the output cannot be parsed, usage is silently skipped —
token tracking is best-effort and never blocks execution.

### Via step config (per-step override)

Set `usage_command` directly on a step to override or supplement the `config.toml`
default:

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_command = "codex"
  usage_command = "codex usage --last --json"
  results = [success, fail]
}
```

Step-level `usage_command` takes precedence over the `[agents.codex]` config.toml
value.

## Verifying the Setup

Run a quick test workflow:

```bash
cloche run develop --prompt "Print hello world"
```

Check logs to confirm Codex is executing:

```bash
cloche logs <run-id>
```

## Troubleshooting

**Codex not found in container**: Make sure your Dockerfile installs `@openai/codex`
via npm and that Node.js is available.

**Authentication errors**: Verify that `CLOCHE_EXTRA_ENV` includes a valid
`OPENAI_API_KEY`. You can check by inspecting the container's environment:

```bash
docker exec <container-id> env | grep OPENAI
```

**No output from Codex**: Since Codex is not a known agent, Cloche passes the prompt
on stdin. Ensure the Codex CLI version you installed supports reading prompts from
stdin, or set appropriate `agent_args`.
