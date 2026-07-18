# Help Channel — Design

**Status:** Draft (RFC)
**Date:** 2026-07-17

## Summary

A first-class channel for in-container agents to request and receive user guidance
mid-run. The agent asks a question through a protocol surface it already understands
(an MCP tool or the `clo` CLI); cloched routes the question to whatever user-side
integrations are configured (CLI inbox, Slack, later Discord/IRC/...); the answer
flows back and the agent continues. If no answer arrives promptly, the run is
**parked** — the container is committed and stopped, and answering later resumes it.

This opens the door to interactive planning tasks, mid-task course corrections, and
"I'm blocked, need input" escalations without the agent knowing or caring which
integration the user has configured.

## Goals

- Agent-facing surface is integration-agnostic: ask a question, get an answer.
- Threaded conversations: a question opens a thread; agent and user can go back and
  forth within it (interactive planning), not just one-shot Q&A.
- Pending questions do not burn a live container indefinitely: park after a grace
  period, resume on answer.
- User-side delivery is a pluggable port. v1 ships the CLI channel (always on) and
  Slack. Adding Discord/IRC/etc. later means implementing one interface.
- Full observability: pending questions visible in `cloche status`, thread history
  persisted in SQLite.

## Non-goals (v1)

- Notify-only (fire-and-forget) alerts — deferred; the port shape allows it later.
- User-initiated unsolicited messages into a running task — deferred (the thread
  model supports appending them; delivery semantics are the open question).
- Rich media in messages (images, files). Text + structured options only.

## Domain model

```go
// internal/domain/help.go
type HelpThread struct {
    ID        string      // thread-<ulid> (stable internal id)
    Channel   string      // cloche-side channel name, e.g. project name ("cloche", "mazd")
    Name      string      // human-addressable name, unique within Channel (see Naming)
    TaskID    string
    AttemptID string
    RunID     string
    StepName  string
    Title     string      // short summary, shown in channel headers / thread list
    State     ThreadState // awaiting_user | awaiting_agent | closed | archived
    CreatedAt, UpdatedAt time.Time
    ArchivedAt *time.Time // set when the owning task completes successfully
}

type HelpMessage struct {
    ID        string
    ThreadID  string
    Author    MessageAuthor // agent | user
    Body      string
    Options   []string      // optional suggested answers (agent messages only)
    AskKey    string        // idempotency key for replay (see Parking)
    CreatedAt time.Time
}
```

State machine: agent message → `awaiting_user`; user reply → `awaiting_agent`;
agent consumes reply (its blocked ask returns, or it asks a follow-up) →
`awaiting_user` again or stays open; the agent/user may close a thread explicitly.

**Archival & retention:** when a thread's owning *task* completes successfully,
all its threads move to `archived` (still readable via `cloche threads show`,
hidden from the default list). A daily daemon sweep deletes archived threads
older than 30 days. Threads of failed/aborted tasks stay in their last state so
they remain available for diagnosis; they archive when a later attempt succeeds.

### Naming

With multiple projects and features in flight at once, threads need stable
human-addressable names, not just ULIDs. Two-level scheme:

- **Channel** = the cloche-side grouping, defaulting to the project name
  (`cloche`, `mazd`, ...). Slack/Discord adapters may map cloche channels to
  platform channels in config.
- **Thread name** = auto-generated slug from the title plus a per-channel
  sequence for uniqueness: `schema-choice-7`, `api-contract-12`.

Full address: `<channel>/<name>`, e.g. `mazd/schema-choice-7`. CLI commands
accept the full address, or the bare name when unambiguous, or the raw thread
ID. External bindings (Slack `thread_ts`) still resolve through `help_bindings`.

## Ports

### HelpStore (persistence)

`internal/ports/store.go` gains:

```go
type HelpStore interface {
    CreateThread(ctx, *domain.HelpThread) error
    AppendMessage(ctx, *domain.HelpMessage) error
    GetThread(ctx, threadID) (*domain.HelpThread, []domain.HelpMessage, error)
    ListOpenThreads(ctx) ([]domain.HelpThread, error)
    SetThreadState(ctx, threadID, state) error
    // Channel bindings: map external ids (e.g. Slack thread_ts) to thread ids.
    BindExternal(ctx, threadID, channelName, externalID string) error
    ResolveExternal(ctx, channelName, externalID string) (threadID string, err error)
}
```

SQLite tables: `help_threads`, `help_messages`, `help_bindings`. Follows the
existing `HumanPollStore` pattern in `internal/adapters/sqlite/store.go`.

### HelpChannel (pluggable user-side integration)

New file `internal/ports/help.go`:

```go
// HelpChannel delivers agent questions to the user over one integration and
// feeds user replies back through the sink. Implementations must be safe to
// run for the daemon's lifetime.
type HelpChannel interface {
    Name() string // "cli", "slack", ...
    // Start runs the channel until ctx is done. Replies are pushed into sink.
    Start(ctx context.Context, sink ReplySink) error
    // Post delivers a new agent message on a thread. Called for every
    // configured channel; errors are logged, not fatal (other channels and
    // the CLI inbox still work).
    Post(ctx context.Context, thread domain.HelpThread, msg domain.HelpMessage) error
}

type ReplySink interface {
    // Reply appends a user message to a thread. First reply to an
    // awaiting_user thread unblocks/unparks the asking run.
    Reply(ctx context.Context, threadID string, body string, via string) error
}
```

The daemon hosts a **HelpRouter** that owns the store, fans `Post` out to all
configured channels, accepts `Reply` from any of them (all replies append to the
thread; the first one after a question unblocks the waiter), and coordinates
blocking/parking (below).

## Agent-facing surface

Both surfaces converge on the same daemon-side `HelpRouter.Ask` call.

### 1. MCP tool (for Claude Code and other MCP-capable agents)

Per the MCP-mode RFC (`2026-05-28-mcp-mode.md`), cloched serves MCP over HTTP at
`/mcp` on the web dashboard port. The help channel is the first implemented tool
on that server (the full inverted-control mode remains future work):

```
tool ask_user
  question: string        (required)
  title:    string        (optional; defaults to first line of question)
  options:  []string      (optional suggested answers)
  thread_id: string       (optional; continue an existing thread)
  ask_key:  string        (optional idempotency key; see replay)
→ { answer: string, thread_id: string, parked: bool }
```

The prompt adapter (`internal/adapters/agents/prompt/prompt.go`) injects an
`--mcp-config` pointing at `http://<CLOCHE_ADDR host>/mcp` with run-identifying
auth (run ID + a per-run token minted at container start, passed via env), so the
daemon knows which run is asking without trusting the tool arguments.

### 2. `clo ask` (for generic/script steps and non-MCP agents)

```
clo ask [--thread <id>] [--option A --option B ...] [--key <ask-key>] "question"
```

Prints the answer to stdout, exit 0. If the run gets parked mid-ask, `clo ask`
exits non-zero with a distinct code (the step is being torn down anyway; on
replay the same call returns the answer instantly — see Parking).

New unary RPC on `ClocheService`:

```proto
rpc AskHelp(AskHelpRequest) returns (AskHelpResponse);
message AskHelpRequest  { string task_id/attempt_id/run_id; string question, title, thread_id, ask_key; repeated string options; }
message AskHelpResponse { string answer; string thread_id; bool parked; }
```

A long-lived blocking unary call is acceptable because the in-place wait is
bounded by `park_after` (below); it never blocks for hours.

## Blocking and parking

Two-phase wait, coordinated by the HelpRouter:

**One ask at a time:** a run may have at most one blocked ask open. A second
`AskHelp`/`ask_user` call while one is pending returns an error telling the
agent to wait for (or fold the new question into) the outstanding one. This
keeps the user from being overloaded and makes parking unambiguous.

**Phase 1 — block in place** (up to `park_after`, default `5m`, configurable per
project and overridable per ask): the ask call blocks on a channel in an
in-process pending map (same pattern as the container pool's `pending` map /
MCP-mode's session store). If a reply lands in time, the ask returns the answer
inline and the agent continues mid-step. Fast path, no lifecycle churn.

**Phase 2 — park**: when `park_after` expires with no reply:

1. The ask call returns `{parked: true}` with explanatory text ("No answer yet.
   The run is being parked; you will receive the answer when it resumes."). This
   keeps the agent transcript well-formed (no dangling tool call).
2. The daemon sends a new `ParkStep{step_name}` variant on the `DaemonMessage`
   oneof. `cloche-agent` terminates the step subprocess and replies
   `StepResult{result: "parked"}`. (`parked` becomes a reserved result name,
   like `done`/`abort`; workflows cannot wire it.)
3. The daemon commits the container (`ContainerCommitter`, tag
   `cloche-park-<run-id>`), stops it, and marks the run **parked** — a new
   run status alongside `waiting`, surfaced in `GetStatus`
   (`parked_thread_id`, `parked_title`).
4. The engine treats the step as suspended, not failed. No wiring fires.

**Resume on reply**: when a user reply arrives on the parked thread:

1. Daemon starts a container from the parked image and re-dispatches
   `ExecuteStep` with `resume = true` (field already exists in the proto).
2. **Prompt adapter**: the adapter captured Claude's `session_id` from the
   stream-json init event before park (stored in run KV as
   `_help.agent_session_id`). On resume it runs `claude --resume <session-id>`
   with the reply delivered as the new user prompt: *"Answer to your question
   in thread `<id>`: <reply>"*. The agent picks up exactly where it left off.
3. **Generic steps** (replay semantics): the step's command re-runs from the
   start. To make this safe, `Ask` is idempotent: an ask with an `ask_key` (or,
   absent a key, a body hash) that already has a later user reply in the same
   thread returns that answer immediately without re-posting. A well-behaved
   script therefore replays cheaply up to the ask site and continues with the
   answer. Steps that cannot replay safely should set `park: false` in the step
   config (ask then blocks in place for the step timeout instead — documented
   trade-off).

Agents without resume support fall back to replay semantics (same as generic).

## User-side channels (v1)

### CLI (always on)

Grouped under `cloche threads` (multiple projects/features run concurrently, so
threads are addressed by `<channel>/<name>`):

- `cloche threads` / `cloche threads list [--all] [--channel <c>]` — open
  threads across all channels (address, task, age, state, title). `--all`
  includes archived.
- `cloche threads show <channel>/<name>` — full transcript.
- `cloche threads reply <channel>/<name> "message"` — append a reply;
  unblocks/unparks the run.
- `cloche threads chat <channel>/<name>` — interactive loop (stream new
  messages, type replies); follows the `cmd/cloche/console.go` stream+raw-loop
  shape. Can land after v1; `list`/`show`/`reply` are the required core.
- `cloche status` shows `parked — awaiting reply: <title> (<channel>/<name>)`.

The CLI channel needs no `Start` loop — it is pull-based over new RPCs
(`ListThreads`, `GetThread`, `ReplyThread`) that hit the HelpRouter directly.

### Slack

`internal/adapters/help/slack/`:

- **Socket Mode** (no inbound public endpoint — fits a laptop/self-hosted daemon).
  Config: bot token + app-level token via env var names in daemon config.
- `Post`: first message of a thread posts to the configured channel
  (`#cloche` by default) with task/run context and option buttons if provided;
  the Slack `thread_ts` is stored via `BindExternal`. Follow-ups post into the
  same Slack thread.
- Replies: message events in a bound Slack thread resolve through
  `ResolveExternal` and feed `sink.Reply`. Option-button clicks reply with the
  option text.

### Configuration

Channels are daemon-level (user infrastructure, not project code), in the daemon
config with per-project override for routing:

```
help {
  park_after = "5m"
  retention  = "720h"   # delete archived threads after 30 days
  channel "slack" {
    channel       = "#cloche"      # default; may map cloche channels 1:1 instead
    token_env     = "SLACK_BOT_TOKEN"
    app_token_env = "SLACK_APP_TOKEN"
  }
}
```

Unknown channel types fail daemon start with a clear error. No channels
configured ⇒ CLI-only, which always works.

## Protocol changes (summary)

- `ClocheService`: new RPCs `AskHelp`, `ListThreads`, `GetThread`, `ReplyThread`.
- `DaemonMessage` oneof: new `ParkStep` variant. `AgentMessage`: none (step
  result `parked` reuses `StepResult`).
- `GetStatus`: `parked_thread_id`, `parked_title` fields (pattern of
  `waiting_step`).
- New reserved step result `parked`; new run status `parked`.
- MCP endpoint `/mcp` on the web dashboard port with the `ask_user` tool and
  per-run bearer auth.

## Observability

- Thread lifecycle events go to the run's log broadcast (question asked, parked,
  reply received, resumed) so `cloche logs -f` tells the story.
- Activity store records ask/reply/park/resume events.
- Web dashboard: open-threads panel with reply box (natural follow-up; not
  gating v1).

## Phasing

1. **Core ask path** — domain types, HelpStore + SQLite, HelpRouter, `AskHelp`
   RPC, `clo ask`, MCP `/mcp` server with `ask_user`, CLI channel
   (`threads list`/`show`/`reply`), block-in-place only (`park_after` accepted but
   long waits just block; container stays up). Fully usable end to end.
2. **Parking** — `ParkStep`, commit/stop, `parked` status, resume-on-reply with
   `claude --resume` + replay semantics for generic steps.
3. **Slack channel** — socket-mode adapter, bindings, option buttons.
   (2 and 3 are independent; can land in either order.)
4. Later: `cloche threads chat`, dashboard panel, notify-only messages,
   user-initiated messages, Discord/IRC.

Versioning: new major feature ⇒ **minor** bump, tagged on the bead ticket per
policy.

## Resolved decisions

- `park_after` defaults to **5m**; configurable per project, overridable per ask.
- CLI is grouped under **`cloche threads`**; threads are addressed by
  `<channel>/<name>` since multiple projects/features run concurrently.
- Threads **archive when the owning task completes successfully** and are
  **deleted 30 days** after archival by a daemon sweep. Failed/aborted tasks
  keep their threads live for diagnosis.
- **One open ask per run** — a second concurrent ask is rejected, to avoid
  overloading the user and to keep parking unambiguous.
