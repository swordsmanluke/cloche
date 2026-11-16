# DRAs (Domain Responsible Agents)

A DRA owns responsibility for one domain of a project (authentication, the
engine, performance...). Design: `docs/plans/2026-11-10-autonomous-loop-design.md`,
Part 5. This page covers the definition on disk, the container, the daemon credential and
wake-ups.

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

- `cloche dra list [--project <dir>]` prints each DRA's name, its session id (`-`
  until the first wake-up), its last wake-up (run id, result, how the session was
  obtained, time) and the charter's first heading.
- `cloche dra wake <name> --message <text> [--project <dir>]` starts a wake-up by
  hand (see below).
- `cloche dra heartbeat <name>` sends the trigger described under "Triggers".
- `cloche dra wait`, `waits` and `wait --cancel` register, list and cancel waits on
  owner-written polls (see "Waits" below and `docs/USAGE.md`).
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
workflow dra-wakeup {
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
to the agent image. `make docker-dra` builds it alone.

It contains `cloche-agent`, `cloche`, `clo` and the model CLI. It has no `go`,
`gcc`, `make`, `git`, no tracker CLI (`bd`, `gh`), no git push credentials and no
tracker data. A DRA reaches the tracker only through the daemon, and asks
questions with `cloche questions ask`, `await` and `read`.

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

Anything else gets `403 egress denied` from the proxy, or no route. The container runs
unprivileged (no `--privileged`, no `NET_ADMIN`) with `no-new-privileges`. The daemon
gRPC port is the same one runs use; narrowing what a DRA may call on it is separate work.

When the run ends the sidecar is stopped and removed, even if the main container is kept
(failed runs, `--keep-container`); a stopped container needs no proxy. The network is removed
with the main container.

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
not a project signal token or the run's `/mcp` token, and neither accepts it.

The credential is `base64url(claims JSON) + "." + hex(HMAC-SHA256(secret, "cloche-dra-v1\0" + payload))`
(`internal/draauth`), signed with the daemon-lifetime secret, so the daemon keeps no
token store: it recomputes the signature on each request and then checks that the run
is still active. The `cloche-dra-v1` prefix keeps a DRA credential from ever being
valid as the run's `/mcp` token, which signs the bare run ID under the same secret.
The rest of the dashboard listener and the daemon's gRPC port have no
authentication yet, so today these limits hold only for a DRA that uses the CLI
it is given; enforcing them against a DRA that goes around it is future auth work. Rulings: CLOCHE-A118, A119, A128.

A wake-up workflow names its DRA with `container { dra = "<name>" }`; the
container runtime then injects the three variables above. Containers that do not
set `dra` get none of them.

A DRA may: list its project's tasks and runs, read a run's status and step
output (runs of its own project only), create a task, update or close a task,
ask, await and read questions (`cloche questions ask|await|read`, routes
`/api/dra/questions/...`, the same qa module slots the project routes use), and
register, list and cancel **its own** waits (`/api/dra/waits...`). The DRA's name
and project always come from the credential, never from the request body.

- A created task **always carries the DRA's domain label** (the DRA's name).
- Update and close are **refused for a task that lacks that label** (the daemon
  reads the task's labels through the tracker's `update-task` phase, `action = get`).
  `find` never reveals another domain's task. A DRA cannot remove its own
  domain label.
- Everything else is refused: stop, delete, resume, shutdown, loop control,
  project purge, signal tokens, another project.

Inside the container `cloche tasks [create|update|find|close]`, `cloche list`,
`cloche status <run>`, `cloche logs <run> --step <s>`, `cloche questions
ask|await|read` and `cloche dra wait|waits` use the credential; other commands
are refused (`questions guide` and `dra list|wake|heartbeat` included).

**Ready (ruling CLOCHE-A160).** A DRA may mark any task in its own domain `ready`,
on create and on update; a person need not promote it. The daily budget cap is the
only limit: a ready task is picked up by the loop like any other, and the loop
does not start runs once the cap is spent. Marking `ready` on a task of another domain, a
missing task, or one without the DRA's domain label is refused (403) and the error
names the DRA's domain; an update may add the domain label and `ready` together to
an unclaimed task.

**Known gap:** the daemon's gRPC port is unauthenticated today (that is how
`clo` and `cloche-agent` work), so refusing `stop`/`delete`/`shutdown` over gRPC
needs gRPC authentication or the network allowlist in the DRA-container work.
This change covers the HTTP API and the CLI.

## Wake-ups

A wake-up is an ordinary recorded run (`dra-wake-<name>`, with token usage like any
other) that the daemon builds in Go: a host workflow dispatching container workflows
in the DRA image, each with one prompt step. The message saying what happened is
written to `.cloche/runs/<run-id>/wake-message.md`, which the container sees; it is a
file so a message containing `{{` is never read as a prompt template. Design:
`docs/plans/2026-11-10-autonomous-loop-design.md`, Part 5; rulings CLOCHE-A117, A130.

```
resume --success--> done        (only when a session id is stored)
resume --fail-----> fresh
fresh  --success--> done
```

- **Session.** Each DRA has a named Claude Code session. The first wake-up runs
  `fresh`, which starts it with `--session-id <uuid>`; later ones run `resume`
  (`--resume <uuid>`). The daemon stores the id and the last wake-up in
  `.cloche/runs/dra-sessions.json` (runtime state, gitignored with the rest of
  `runs/`); `cloche dra list` reads it.
- **Session files** live in a Docker volume per DRA and project
  (`cloche-dra-<project hash>-<name>`), mounted at `/home/agent/.claude`, so they
  survive between runs.
- **Lost session.** If `resume` fails (the session is gone, or the volume was
  lost) the run falls back to `fresh`: a new session, rebuilt from `charter.md` and
  `notes.md`, whose id replaces the stored one. A `resume` that fails because the
  agent itself reported failure also falls back; that costs one extra session, not
  correctness, since the notes are the memory.
- **Notes.** Both prompts tell the agent to read `notes.md` first and update it
  before stopping. The project is copied in from the live tree (a snapshot of `HEAD`
  would lack an untracked `notes.md`), and `notes.md` is copied back to
  `.cloche/dras/<name>/notes.md` when the container is cleaned up, whether the run
  succeeded or failed. A DRA never changes code, so nothing else is extracted.
- **One at a time.** A second wake-up of a DRA that already has one running is
  **refused**, not queued: the message names the running run. A queue would replay a
  stale message after the DRA has already looked at the current state.
- **Budget.** The launch gate applies: automatic wake-ups (`WakeDRA` with
  `manual = false`, used by every trigger below) are not started while the daily
  cap is exceeded. `cloche dra wake` is hand-started, so it still runs, with the
  usual warning.

## Triggers

A DRA is woken by events, never kept running. Design:
`docs/plans/2026-11-10-autonomous-loop-design.md`, Part 5; ruling CLOCHE-A123.

**Domains are labels.** A task whose labels include a DRA's name belongs to that
DRA. A task has one owner: with two matching labels the first (in label order) is
used and the daemon logs the ones it ignored.

| Trigger | Fires when |
|---|---|
| Task result | a task in the DRA's domain finishes, fails or parks (a task stopped on request is not a result) |
| Signal | a signal arrives with `domain` naming the DRA. The default `handle-notification` also puts the domain on the task as a label. The wake-up is sent after the handler ran, so the task it created already exists. Occurrences that join a running handler (same source and key) are not separate events |
| Wait | a wait the DRA registered ends: its poll reported a result, the poll failed, or the wait timed out (see "Waits") |
| Heartbeat | `cloche dra heartbeat <name>`: wakes the DRA if it has not been woken, for any reason, for the heartbeat interval |

The `ruling` event kind is gone: `cloche dra ruling` was removed and the daemon
refuses `ruling` as a notification kind. A DRA that wants a ruling asks with
`cloche questions ask` and waits on it with `cloche dra wait questions --ids ...`.

### Heartbeat

Time triggers are not part of the DSL yet (CLOCHE-A114), so the heartbeat uses the
existing poll-step timer: a host workflow with a `poll` step whose script runs
`cloche dra heartbeat <name>` and reports `waiting`. Calling it more often than the
interval is harmless; the daemon decides whether a wake-up is due. Set the interval
per project in `.cloche/config.toml` (default 4 hours; `"off"` disables):

```toml
[dras]
heartbeat = "4h"
```

### Batching

An event never starts a run by itself. It joins the DRA's inbox
(`.cloche/runs/dra-events.json`, runtime state, so a daemon restart keeps it) and the
daemon delivers the whole inbox as **one** wake-up message, in order:

- while the DRA is awake the events wait; when its wake-up ends they go out together,
- while the budget gate is closed they wait; the daemon retries every 30 seconds and
  they go out together once it opens,
- nothing is dropped and no event starts a second run. If a wake-up itself fails its
  events are not replayed (a failing DRA would loop); the next heartbeat covers it.

### Waits

A DRA that has nothing to do until something happens ends its wake-up and registers
a **wait**. It names a poll; it never supplies script text. Polls are written by the
project's owner, run on the host, and live at
`.cloche/dras/<name>/polls/<poll>.sh` (the file must be executable; it is run
directly, so it needs a shebang). There is also one built-in poll, `questions`, which runs
`cloche questions await <ids>` and is ready (`answered`) once none is open.

```
cloche dra wait build-done --arg branch=main --interval 5m --note "main build"
cloche dra wait questions --ids Q14,Q15 --note "rulings"
cloche dra waits
cloche dra wait --cancel w-3
```

- **Resolution.** The name must match a script the owner wrote (a plain lowercase
  name, never a path) or `questions`. Anything else is refused:
  `unknown poll no-such-poll; this DRA has: build-done, deploy-ready`.
- **Arguments** reach the script as `CLOCHE_WAIT_ARG_<KEY>` environment variables
  (key upper-cased; names are letters, digits and `_`, values text under 4 KiB). The
  script is executed as an argument vector, never through a shell, so a value like
  `main; touch x` is just that text. For `questions` the ids become arguments of
  `cloche questions await`; an id must be a plain word.
- **Result.** The poll-step protocol applies: a `CLOCHE_RESULT:<name>` marker is the
  result (ready), exit 0 without a marker is "not yet" (poll again after the
  interval), non-zero without a marker is a failure. The first poll is immediate.
- **Limits.** 20 pending waits per DRA, a 1 minute minimum interval (default 5m), a
  72 hour default timeout. Over a limit, registration is refused with the reason.
- **Own waits only.** A DRA lists and cancels only the waits it registered; another
  DRA's wait looks like one that does not exist.
- **Wait event.** Each wait ends with exactly one `wait` event in the DRA's inbox,
  carrying the note, the outcome (`ready` with the result name, `failed`, or
  `timeout`) and the poll's last output (truncated). It is batched under the rules
  above, so it rides in the next wake-up with any other pending events. A cancelled
  wait sends none.

**Persistence and polling.** Pending waits are kept in `.cloche/runs/dra-waits.json`
of the project (next to `dra-sessions.json`), so they survive a daemon restart: on
start the daemon reads every known project's waits and resumes polling them. A wait
that came due while the daemon was down is polled at the first pass after it starts.
Each poll invocation is a recorded host run named `dra-wait-<name>` with one `poll`
step; it shows in `cloche list` like any run. The step ends `ready` (the script printed
a `CLOCHE_RESULT` marker, whose name goes into the wait event), `pending` (exit 0 with
no marker: not yet) or `failed` (non-zero exit with no marker). `failed` aborts that
run and ends the wait with a `failed` event. The daemon runs the script directly, never
through a shell.

### No self-wake

Signals whose source is `dra:<name>` do not wake the DRA that sent them. Task results
always wake the owner, including results of tasks the DRA itself created: a run
ending is never caused directly by the DRA's own API call, so there is no loop to break.
