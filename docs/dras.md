# DRAs (Domain Responsible Agents)

A DRA owns responsibility for one domain of a project (authentication, the
engine, performance...). Design: `docs/plans/2026-11-10-autonomous-loop-design.md`,
Part 5. This page covers the definition on disk; sessions and wake-ups come later.

## Layout

```
.cloche/dras/<name>/
  charter.md   # required: the domain, what it may do, the skills it uses
  notes.md     # the DRA's memory; created empty if missing
```

Zero DRAs is the default and changes nothing. The directory name is the
domain, and follows the same rule as a label: lowercase letters, digits and `-`.

## Notes and git

**Cloche never commits `notes.md`.** Whether notes are tracked in git or
ignored (e.g. `.cloche/dras/*/notes.md` in `.gitignore`) is the project's choice.

## Commands

- `cloche dra list [--project <dir>]` prints each DRA's name, whether a session
  exists (`-` until sessions are implemented) and the charter's first heading.
- `cloche validate` reports a DRA with no `charter.md` or an invalid name.

## The locked-down container

A DRA (Domain Responsible Agent) manages work for a domain. It never changes code or
stops or deletes runs, and it runs in a container with tighter limits than a workflow
run (design: `docs/plans/2026-11-10-autonomous-loop-design.md`, Part 5; ruling
CLOCHE-A118). This page covers the container; wake-ups and charters are documented
with the DRA feature itself.

### Selecting the profile

A workflow (or a single step) selects the locked-down profile in its `container {}`
block:

```
workflow "dra-wakeup" {
  container {
    profile = "dra"
    # optional:
    # image = "cloche-dra:latest"                    # default for this profile
    # notes = ".cloche/dras/performance/notes.md"    # default .cloche/dras/<workflow>/notes.md
  }
  ...
}
```

Unknown profile names fail the step; they never fall back to an unrestricted container.

### The image

`docker/cloche-dra/Dockerfile` builds `cloche-dra:latest`. `make install` builds it next
to the agent image when `SAGA_URL` (a tarball URL for a static `saga` binary) is set;
`make docker-dra SAGA_URL=...` builds it alone.

It contains `cloche-agent`, `cloche`, `clo`, `saga` and the model CLI. It has no `go`,
`gcc`, `make`, `git`, no tracker CLI (`bd`, `gh`), no git push credentials and no
tracker data. A DRA reaches the tracker only through the daemon.

### Read-only project

The project is copied in as usual, then made root-owned and non-writable for the agent
user. The one writable project path is the DRA's `notes.md`: created if missing, and
copied back to the same path in the host project when the attempt ends. The file can be
edited in place but not replaced, and nothing else in its directory is writable. (The
run's own `.cloche/runs` and `.cloche/output` state stays writable, as for any run.)

### Network

The container joins a per-run Docker `--internal` network with no route to anywhere. An
egress sidecar (`cloche-agent egress`, from the same image) is the only other member and
the only way out:

| Destination | How |
|---|---|
| Model API (`api.anthropic.com:443`) | HTTPS CONNECT proxy (`HTTPS_PROXY`), allowlist enforced |
| Daemon gRPC port | TCP forward, `host.docker.internal` resolves to the sidecar |
| Saga port | TCP forward; set `CLOCHE_SAGA_ADDR=host:port` in the daemon's environment |

Anything else gets `403 egress denied` from the proxy, or no route. The container runs
unprivileged (no `--privileged`, no `NET_ADMIN`) with `no-new-privileges`. The daemon
gRPC port is the same one runs use; narrowing what a DRA may call on it is separate work.

### Tests

`internal/adapters/egress` tests the allowlist (a CONNECT to a host outside it is
refused and never dialed). `internal/adapters/docker` tests the Dockerfile has no build
tools, and, when Docker and the image are available, that the built image lacks
`go`/`gcc`/`make` and that writes to the project fail while `notes.md` is writable.

## Daemon access (the wake-up credential)

A DRA container reaches the daemon only through its **DRA API** (`/api/dra/...`
on the dashboard listener), with a credential the daemon mints for each wake-up
(`CLOCHE_DRA_TOKEN`, with `CLOCHE_DRA_URL` and `CLOCHE_DRA_NAME`). The credential
names the wake-up's run, the project and the DRA, and stops working the moment
that run leaves an active state (finished, failed, cancelled or parked). It is
not the dashboard token, a project signal token or the run's `/mcp` token, and
none of those accept it. Rulings: CLOCHE-A118, A119, A128.

A wake-up workflow names its DRA with `container { dra = "<name>" }`; the
container runtime then injects the three variables above. Containers that do not
set `dra` get none of them.

A DRA may: list its project's tasks and runs, read a run's status and step
output (runs of its own project only), create a task, and update or close a task.

- A created task **always carries the DRA's domain label** (the DRA's name).
- Update and close are **refused for a task that lacks that label** (the daemon
  reads the task's labels through the tracker's `update-task` phase, `action = get`).
  `find` never reveals another domain's task. A DRA cannot remove its own
  domain label.
- Everything else is refused: stop, delete, resume, shutdown, loop control,
  project purge, signal tokens, another project.

Inside the container `cloche tasks [create|update|find|close]`, `cloche list`,
`cloche status <run>` and `cloche logs <run> --step <s>` use the credential;
other commands are refused.

**Open question (needs a ruling; not guessed):** does a DRA-created task get the
`ready` label by itself? `ready` hands a task to the loop, which spends budget.
Until decided, a DRA **cannot** set `ready` (on create or update); a person
promotes the task.

**Known gap:** the daemon's gRPC port is unauthenticated today (that is how
`clo` and `cloche-agent` work), so refusing `stop`/`delete`/`shutdown` over gRPC
needs gRPC authentication or the network allowlist in the DRA-container work.
This change covers the HTTP API and the CLI.
