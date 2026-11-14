# Safety Guide

This document covers good practices for using Cloche securely. The central concern is
the **Lethal Trifecta** of LLM-powered agent usage: when a system simultaneously has
(1) access to private data, (2) exposure to untrusted content, and (3) the ability to
communicate externally, it becomes vulnerable to prompt injection attacks that can
exfiltrate sensitive information. Cloche's architecture is designed to let you break this
trifecta, but only if you configure it correctly.

## The Lethal Trifecta

Any system that combines all three of the following is at risk:

1. **Access to private data** — The agent can read source code, credentials, API keys,
   internal documentation, or customer data.
2. **Exposure to untrusted content** — The agent processes inputs that an attacker could
   influence: issue descriptions, pull request comments, dependency READMEs, web pages,
   or fetched API responses.
3. **Ability to communicate externally** — The agent can make HTTP requests, send emails,
   push to remote repositories, or otherwise transmit data outside the local environment.

When all three are present, a prompt injection hidden in untrusted content can instruct
the agent to exfiltrate private data through the external communication channel. The
defense is to remove at least one leg of the trifecta.

Cloche helps by isolating agent work inside containers where you control filesystem
access and network egress. But the host side of Cloche has no such isolation — host-side
agent steps run with the daemon's full OS privileges. The recommendations below focus on
keeping host-side exposure minimal and container-side access locked down.

## Minimize Host-Side Agent Usage

Host workflows (those with a `host { }` block) run directly on the host machine. Agent
steps in host workflows inherit the daemon's permissions: full filesystem, full network,
all environment variables.

**Prefer container workflows for agent work.** The `workflow` step type in host workflows
dispatches work to an isolated container. Use it:

```
workflow main {
  step prepare-prompt {
    run = ".cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> abort
}
```

In this pattern, the host workflow uses a *script* step (deterministic, no LLM) to
prepare the prompt and a *workflow* step to dispatch it to a container. No agent step
runs on the host.

**If you must use host-side agent steps**, limit what the agent can access:

- Run the daemon under a dedicated service account with restricted filesystem
  permissions rather than your personal user account.
- Avoid setting broad environment variables (cloud credentials, database URLs) in the
  daemon's environment. Only export what is strictly needed.
- Keep host workflows simple — use them for orchestration (scripts and workflow dispatch),
  not for direct agent work.

## Network Allowlisting

Containers should have the minimum network access needed for their task. The workflow DSL
accepts a `network_allow` list in the `container {}` block to declare intended egress
restrictions:

```
workflow develop {
  container {
    image         = "my-project:latest"
    network_allow = ["api.anthropic.com", "github.com"]
  }

  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  implement:success -> done
  implement:fail    -> abort
}
```

> **Note:** `network_allow` is currently parsed by the DSL but not enforced at runtime —
> containers run with unrestricted network access. Until enforcement is implemented, use
> the Docker-level controls described below to actually restrict egress. Declaring
> `network_allow` in your workflow is still recommended as documentation of intent and to
> be ready when enforcement lands.

A good allowlist includes only:

- The AI provider API endpoint (e.g. `api.anthropic.com`)
- Your git remote for pushing results (e.g. `github.com`)
- Language-specific package registries only if the workflow installs dependencies

Everything else — documentation sites, third-party APIs, general internet — should be
excluded unless the task specifically requires it. The agent may be less capable without
web access, but the security tradeoff is worth it for tasks involving sensitive code.

> **Exception:** the locked-down DRA profile (`container { profile = "dra" }`) *does*
> enforce its allowlist, through an egress sidecar on a Docker `--internal` network.
> See [Locked-down DRA containers](#locked-down-dra-containers) below and `docs/dras.md`.

### Docker Network Isolation

Since `network_allow` is not yet enforced, Docker-level network restrictions are
currently the primary mechanism for limiting container egress. Configure them in your
project's Dockerfile or via Docker runtime options:

**Drop all network by default.** If your workflow does not need any network (e.g. pure
code editing with tests), override the container's network at the Docker level:

```dockerfile
# In your .cloche/Dockerfile — no network-dependent steps
FROM cloche-base:latest
# Install all dependencies at build time so no runtime downloads needed
COPY requirements.txt .
RUN pip install -r requirements.txt
```

When all dependencies are baked into the image, the container needs no outbound access
except for the AI provider API.

**Restrict DNS resolution.** Containers use Docker's embedded DNS by default. For
tighter control, you can specify DNS servers that only resolve your allowlisted domains,
or use a DNS-based firewall.

**Avoid `--privileged` and `--cap-add`.** Cloche containers do not need elevated
capabilities. The container starts as root only to fix ownership, then drops privileges
(`gosu agent`), so the agent process runs as an unprivileged `agent` user. Never run Cloche
containers with `--privileged` or add capabilities like `NET_ADMIN` unless you have a
specific, justified need.

## Filesystem Isolation

Cloche containers get a *copy* of your project, not a bind mount of it. (The one
exception is the run directory `.cloche/runs/<runID>`, which is bind-mounted at
`/workspace/.cloche/runs/<runID>`.) This is a deliberate security choice:

- The agent cannot modify your host filesystem directly.
- Changes are only extracted back via `docker cp` to a git branch after the run
  completes.
- You review the changes in a branch before merging.

**Keep this model intact.** Avoid using `CLOCHE_EXTRA_MOUNTS` to bind-mount sensitive
host directories into containers. If you need additional files in the container, prefer
the `.cloche/overrides/` mechanism, which copies files at container start time.

**Audit override files.** Everything in `.cloche/overrides/` is applied on top of
`/workspace/` in the container, including `*.merge.*` files, which are merged into
project config files. Do not place credentials or sensitive configuration in
this directory — it is checked into version control. Override files are left out of
result branches unless the agent edits them, so an override never merges back into the
project by itself.

## Credential Handling

**API keys.** The `ANTHROPIC_API_KEY` environment variable is automatically passed into
containers. Other environment variables must be explicitly passed via `CLOCHE_EXTRA_ENV`.
Only pass what the agent needs.

**Claude credentials.** Cloche copies `~/.claude/.credentials.json` into containers for
agent authentication. Autonomous runs also receive a `settings.json` reduced to its
auth/env keys (`env`, `apiKeyHelper`, `forceLoginMethod`, `forceLoginOrgUUID`,
`awsAuthRefresh`, `awsCredentialExport`, `otelHeadersHelper`); host hooks, permissions,
model and plugin settings are not forwarded. Interactive containers get the full
`settings.json`, `settings.local.json` and `~/.claude.json`. Files are copied (not
mounted) so concurrent runs do not conflict. Be aware that these files are present in
the container filesystem during the run.

**Do not bake secrets into Docker images.** Use environment variables or runtime
injection rather than embedding API keys, tokens, or passwords in your Dockerfile or
`.cloche/overrides/`.

```
# Good: pass at runtime
CLOCHE_EXTRA_ENV=DATABASE_URL=postgres://... cloche run develop

# Bad: in Dockerfile
ENV DATABASE_URL=postgres://...
```

## Signal Intake Token

The dashboard's HTTP listener has no authentication of its own: anyone who can reach
the port can stop, delete and start runs. Bind it to loopback (`http = "127.0.0.1:8080"`)
unless you trust the network. The exceptions carry their own credential: signals and
`tasks/create|update` (below), `/mcp` (per run) and the DRA API (per wake-up).

`POST /api/projects/{name}/signals` requires a per-project bearer token. A signal can create a
tracker task, and a task spends budget, so anyone who can post one can direct your spend
(and, through the task text, steer the agent that works on it; see Prompt Hygiene).

- The token is generated on first use and stored in `~/.config/cloche/signal-tokens/`
  with mode `0600`. Treat it like a password: keep it in your sources' secret stores, not
  in repos, Dockerfiles or `.cloche/overrides/`.
- `cloche signals token` shows it; `cloche signals token --rotate` replaces it
  immediately. Rotate if it leaks, or when someone who had it leaves.
- Comparison is constant-time. An unknown project and a wrong token get the same `401`,
  so project names can't be probed.
- The token travels in a header over plain HTTP unless you terminate TLS in front of the
  dashboard. Don't expose the listener to untrusted networks without that.
- Signal text (`title`, `evidence`) is untrusted input that becomes a task body. The
  default workflow copies it unchanged; apply the Prompt Hygiene advice before feeding it
  to an agent.

## Prompt Hygiene

The content of prompts is the primary vector for injection attacks. Untrusted content
that reaches an agent's prompt can instruct it to take unintended actions.

**Separate trusted and untrusted content.** Prompt templates in `.cloche/prompts/` are
trusted — you write and review them. User requests (task descriptions, issue bodies) are
untrusted — they come from external sources. Cloche's prompt assembly concatenates these,
so the agent sees both. Keep the trusted template's instructions clear and explicit to
reduce the chance of injection overriding them.

**Validate external inputs in script steps.** Use host-side script steps to sanitize or
filter task descriptions before passing them to agent steps. For example,
`.cloche/scripts/prepare-prompt.sh` can strip HTML, limit length, or reject suspicious
patterns before printing to stdout.

## Review Before Merge

Cloche extracts results to git branches, not directly to your main branch. Always review
the agent's changes before merging:

- Check for unexpected file modifications outside the task scope.
- Look for new dependencies, network calls, or credential usage that the task did not
  require.
- Verify that no files were added that could serve as persistence mechanisms (cron jobs,
  git hooks, CI config changes).

This review step is your final safeguard. Automated agents are powerful but not
infallible — treat their output with the same scrutiny you would give any external
contribution.

## Locked-down DRA containers

A DRA (Domain Responsible Agent) is a long-lived agent, the riskiest kind, so its wake-up
runs under tighter limits than a workflow run (see `docs/dras.md`):

- **Its own image** (`cloche-dra`): `cloche-agent`, `cloche`, `clo`, `saga` and the model
  CLI. No compilers, `make`, `git`, tracker CLI, push credentials or tracker data.
- **Read-only project.** Everything under `/workspace` is root-owned and not writable by
  the agent user, except the DRA's own `notes.md` (copied in at start, back out at end).
- **Enforced network allowlist.** The container joins a Docker `--internal` network with
  no route out. An egress sidecar relays only the model API (HTTPS CONNECT) and the
  daemon and Saga ports on the host (TCP forwards). No `--privileged`, no extra
  capabilities, `no-new-privileges`.
- `CLOCHE_EXTRA_MOUNTS` and `CLOCHE_EXTRA_ENV` are not applied to these containers.
- **Fails closed.** If any root-side lock-down step fails, the container exits non-zero
  without starting the agent, and the run fails with an error saying the lock-down could
  not be applied (with the container's output). Only the chown of `/home/agent/.claude`
  may fail silently.

## Summary

| Risk | Mitigation |
|------|-----------|
| Host-side agent has full access | Use container workflows for agent steps; keep host workflows to scripts and dispatch |
| Unrestricted network in container | Declare `network_allow`; enforce with Docker-level network controls until runtime enforcement lands |
| Long-lived DRA agent | `container { profile = "dra" }`: own image, read-only project, enforced network allowlist |
| Sensitive files in container | Rely on copy-based isolation; avoid bind mounts; audit overrides |
| Credentials leaking | Pass via env vars at runtime, not in images or overrides |
| Prompt injection | Sanitize untrusted inputs in script steps; keep prompt templates explicit |
| Signal endpoint abuse | Per-project bearer token (`cloche signals token --rotate`); keep the listener off untrusted networks |
| Malicious agent output | Review branches before merging; check for unexpected changes |
