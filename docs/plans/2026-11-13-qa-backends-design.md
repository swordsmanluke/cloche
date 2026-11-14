# Questions and Answers: Pluggable Backends, One-Line Binds, and DRA Waits

**Date:** 2026-11-13
**Status:** Draft. Nothing here is built.
**Decision record:** Saga feature `cloche/qa-backends`, questions CLOCHE-Q130 to
CLOCHE-Q145, all answered. Q130 to Q140 were decided in conversation with Lucas on
2026-11-13 and recorded as judge answers CLOCHE-A133 to CLOCHE-A143. Q141 to Q145
were answered by Lucas in Saga (CLOCHE-A145 to CLOCHE-A149); CLOCHE-A150 links Q145
to CLOCHE-A109 as an amendment.
**Supersedes:** Part 2 ("Judge") of
[`2026-11-10-autonomous-loop-design.md`](2026-11-10-autonomous-loop-design.md) where
the two differ, and the `saga-judge` module that implemented it. The "ruling" wake-up
trigger of Part 5 is replaced by DRA waits (Part 7 below).

## Problem

An agent sometimes needs an answer it cannot work out: a decision of taste, a
permission, a requirement nobody wrote down. Today cloche supports that through the
`saga-judge` module. It works, but it is Saga all the way through:

- A workflow that asks must set `saga_batch_file` and read `saga_batch_id` and
  `saga_outcome`, so every caller names Saga.
- The question file is defined as "the JSON `saga ask` takes".
- The judge's prompt is half policy and half literal `saga` commands, and the judge
  lives inside the Saga module.
- The DRA image bakes in the `saga` binary, and the locked container's network
  allowlist carries a Saga address (`CLOCHE_SAGA_ADDR`).
- Batches, the held mark, send-back and refuse-if-answered are in cloche's contract
  because Saga has them, not because cloche needs them.

What cloche needs is smaller: *an agent asked a question; a judge may settle it from
the record; otherwise a human answers.* Where the questions live is a detail. A Notion
page with an "Open Questions" section, an Obsidian vault, or Saga should all work.

Separately, a DRA that asks a question has no way to sleep until the answer arrives.
The `cloche dra ruling` trigger exists but nothing calls it.

## Goals

1. Cloche knows a question, an answer, and three operations. Nothing else.
2. A project picks its backend with one line in `project.cloche`.
3. A backend is a module: three small workflows plus instructions for agents.
4. The judge is generic. It learns the backend from the module's instructions.
5. A DRA can end its wake-up and be woken when something it is waiting on is ready.

## Non-goals

- A Notion or Obsidian module. They are used below to test the design, not built.
- A zero-dependency default backend. Without a bound backend, asking fails with a
  message that says what to bind.
- Tracking judge decisions inside cloche. The backend's own history is the record
  (Saga's rulings, a Notion page's history and authors, `git blame` in a vault).
- Held and sent-back as cloche concepts. A backend may have them; cloche does not.
- Atomic "answer only if still open". One judge runs per ask, and a judge never
  replaces a human's answer, so there is no second writer to guard against.

## Overview: what the user sees

A backend is a module directory:

```
.cloche/modules/saga/
  module.toml          # requires = ["saga", "jq"]
  README.md
  qa.cloche            # ask, await-answer, read-answer
  scripts/
  judge.md             # for the judge: what this tool is, how to learn its interface
  asking.md            # optional, for agents that ask: what makes a good question here
```

It is chosen, and configured, in `project.cloche`:

```
bind qa = saga {
  env.SAGA_PROJECT = "cloche"
}
```

A workflow asks with two ordinary host steps:

```
step ask   { run  = "cloche questions ask --file \"$(cloche get questions_file)\"" ... }
step await { poll = "cloche questions await" interval = "1m" results = [answered, timeout] }
```

A DRA asks with the same command, then waits (Part 7).

## Part 1: The question model and the `qa` contract

A question is an **id**, the **text asked**, and an **answer** that is present or
not. There are no states, no batches and no author kinds in cloche.

Three slots make up the contract. Each is a host workflow, like the tracker phases.

| Slot | `kv:` | `out:` | Behaviour |
|---|---|---|---|
| `ask` | `questions_file` | `question_ids` | Posts every question in the file. Writes the new ids, comma-separated. |
| `await-answer` | `question_ids` | `questions_open` | **One non-blocking check.** Writes the ids that still have no answer (empty when all are answered). |
| `read-answer` | `question_id` | `answer_path` | Writes the answer text to a file. Ends in `fail` when the question has no answer. |

- **Waiting on several questions** is the caller holding a list of ids. A batch is a
  backend convenience: the Saga module may use one inside `ask`, and nobody else
  sees it.
- **`await-answer` does not block** (CLOCHE-A145). Blocking belongs to the caller's `poll` step (or
  a DRA wait), so one script serves both.
- **When a human and a judge both answered**, `read-answer` returns the human's. This
  is the only place "who answered" matters to cloche, and each backend tells them
  apart its own way.
- **There is no `reframe` outcome.** "This question is wrong" is an answer. The agent
  that asked reads it and decides whether to ask again.

### The question file

The shape is what `saga-judge` uses today; it is now cloche's own format.

```json
{"title": "...", "context": "...",
 "questions": [{"prompt": "...", "detail": "...",
                "options": [{"label": "...", "description": "...", "recommended": true}]}],
 "backend": {"feature": "cloche/qa-backends"}}
```

`backend` is optional and opaque: cloche passes it through, and the bound module
reads what it understands. It carries settings that vary per ask. Settings that are
the same for the whole project go on the bind (Part 3).

## Part 2: Group binds

A bind is per slot today, which is why the tracker takes six lines. A **group** is a
built-in name for a set of slots:

| Group | Slots |
|---|---|
| `qa` | `ask`, `await-answer`, `read-answer` |
| `tasks` | `list-tasks`, `claim-task`, `close-task`, `release-task`, `create-task`, `update-task` |

`bind qa = saga` means: for any slot in `qa`, use the `saga` module's export of the
same name.

- **A per-slot bind wins** over a group bind in the same scope. Scope order and
  `bind!` are unchanged.
- **The target must be a module.** `module:workflow` and project-workflow targets are
  errors on a group bind.
- **`cloche validate` checks completeness**: a module bound to a group that lacks one
  of its exports is an error naming the missing ones.
- **Group names are reserved.** A workflow or slot named `qa` or `tasks` is a
  validation error.
- **The six-line tracker form keeps working.** `bind tasks = beads-tasks` is the
  short form.

The change is in `ResolveSlot` (`internal/module/resolve.go`): a bind whose slot is
a group name matches any member slot, and the module export is looked up by the
requested slot, not the bind's.

## Part 3: Settings on a bind

Modules have no settings mechanism today; each reads the daemon's environment. One
daemon serves many projects, so a Notion page id or a Saga project name cannot live
there. Settings go on the bind:

```
bind qa = saga {
  env.SAGA_PROJECT = "cloche"
}
```

- **Literals only.** `env.NAME = "value"`. A bind has no calling workflow, so the
  `from` form that step `with` blocks have is not allowed.
- **A group bind's settings apply to every slot in the group.**
- **Precedence:** daemon environment, then bind settings (outer scope to inner), then
  a step's own `with` block.
- **A bind setting satisfies a contract's `env:` requirement.**
- **No secrets.** `project.cloche` is committed. Tokens stay in the module's
  `credentials` list. `cloche validate` warns when a bind sets a variable the module
  lists as a credential.

A brace after a bind target is not valid today, so existing files parse as before.
The daemon already carries a step's `with` environment down to the child run
(`module.WithEnv`); bind settings join it at the two places a slot is resolved
(`internal/adapters/grpc/executor.go` and `internal/host/phases.go`).

## Part 4: Agent instructions in the module

Two files, found by name. Neither is a slot.

- **`judge.md`** tells the judge what the tool is and how to learn its interface. For
  Saga: "run `saga guide` and `saga --help`". For a page-based backend: the page
  layout and the API calls. It is appended to the built-in judge prompt.
- **`asking.md`** (optional) tells an asking agent what makes a good question for
  this backend. Asking agents only use `cloche questions`, so it holds conventions,
  not the backend's interface.

`cloche questions guide` prints `asking.md`; `cloche questions guide --judge` prints
`judge.md`. A module bound to `qa` with no `judge.md` gets a validation warning, and
no judge is started for its questions.

## Part 5: `cloche questions`

One way in, through the daemon, the way `cloche tasks create` works today.

```
cloche questions ask --file <path> [--no-judge] [--project <dir>]   # prints the ids
cloche questions await [<id>...]                                    # poll-script protocol
cloche questions read <id>                                          # prints the answer
cloche questions guide [--judge]
```

- **`ask`** runs the bound `ask` slot, stores the ids in the run's `question_ids` key
  when called from a step, and then starts one judge run for those ids.
- **`await`** runs `await-answer` once. It prints `CLOCHE_RESULT:answered` when no
  question is open and nothing otherwise, so it can be a `poll =` command as is. With
  no ids it reads `question_ids`.
- **`read`** runs `read-answer` and prints the file. Exit 1 when there is no answer.
- **HTTP:** `POST /api/projects/{name}/questions/ask|await|read`, with the project's
  signal token, next to the `tasks/create|update` routes.
- **Who calls it:** host workflow steps and DRAs. A container workflow asks through a
  host step, as today.

One config key, in `.cloche/config.toml`:

```toml
[questions]
judge = true    # false: every question goes straight to a human
```

## Part 6: The judge

A built-in host workflow named `judge`, replaceable by a project workflow of the same
name (as `handle-notification` is). Its prompt is generic rules plus the bound
module's `judge.md`.

The rules:

- (CLOCHE-A148) Answer from supporting information you can cite: an existing question or answer in
  the backend, an intent entry under `.cloche/intent/`, or project documentation such
  as a design doc. The question may be new; it does not need an earlier ruling on the
  same question. Cite every source so a human can check it (an id, or a file and
  section). Never answer from your own reasoning alone.
- Answer only questions that have no answer. Never replace a human's answer.
- When nothing settles the question, or you are not sure, leave it unanswered. An
  unanswered question is one waiting for a human. If the backend has a way to mark
  "a judge looked at this", `judge.md` says how; using it is optional.
- If the question rests on a false premise, say so as the answer.
- Permission requests are in scope. Leave them unanswered unless an earlier ruling
  on the same kind of request clearly covers this one.

How it runs:

- **One judge per ask.** `cloche questions ask` is the only place a judge starts, so
  two judges never work the same question.
- **It is a recorded run** with token usage, and the budget launch gate applies. When
  the gate is closed the judge does not start and the questions wait for a human.
- **It needs the backend directly** (the backend's CLI or API, and its credentials),
  so it is a host agent step. It is the one actor that touches the backend; everyone
  else goes through `cloche questions`.
- **A failed judge changes nothing.** The questions stay unanswered.
- **Learning from corrections** is the backend's job: when a human overrules a judge,
  the next judge finds the human's answer when it searches. `judge.md` says how to
  search so that corrections are followed.

## Part 7: DRA waits

A DRA is not a running process. Today it is woken by task results, signals, a
heartbeat, and a "ruling" trigger that nothing calls. It needs a general way to say
*wake me when this is ready* and stop.

A **wait** is a poll the daemon runs on the DRA's behalf, with poll-step semantics.
The DRA says which poll to run; it cannot create one.

```
cloche dra wait questions --ids Q14,Q15 [--interval 5m] [--timeout 72h] --note "schema choice"
cloche dra wait <poll-name> [--arg key=value ...] ...
cloche dra waits                 # list this DRA's pending waits
cloche dra wait --cancel <id>
```

- **Poll scripts run on the host, so a DRA cannot write them** (CLOCHE-A149). A poll usually needs
  what only the host has (the backend's CLI, credentials, the network), and a DRA
  runs in a locked container that must not be able to make the host run something
  it wrote. A DRA names a poll, and the name resolves to:
  1. `.cloche/dras/<name>/polls/<poll-name>.sh`, written by the project's owner (the
     project is read-only to the DRA), or
  2. the built-in `questions`, which runs `cloche questions await` on the given ids.
- **An unknown poll name is refused** when the wait is registered, with the list of
  polls the DRA has.
- **The script speaks the poll-step protocol.** A `CLOCHE_RESULT:<name>` marker means
  ready. Exit 0 with no marker means not yet. A non-zero exit means the wait failed.
  Arguments reach the script as `CLOCHE_WAIT_ARG_<KEY>` variables, never through a
  shell.
- **Ready, failed and timed out are all events.** Each joins the DRA's inbox as a
  `wait` event carrying the note, the result name and the script's last output. The
  existing rules then apply: events are batched into one wake-up message, wait while
  the DRA is awake or the budget gate is closed, and never start a second run.
- **Waits outlive wake-ups.** A DRA woken for another reason keeps its pending waits.
  They are stored with the rest of the DRA's runtime state under `.cloche/runs/`, so
  a daemon restart keeps them.
- **Limits** (CLOCHE-A146, as a starting place): at most 20 pending waits per DRA;
  interval no shorter than 1 minute; default timeout 72 hours. A poll costs no
  tokens; the wake-up it causes is budgeted like any other.

Implementation: a wait is an ordinary recorded host run (`dra-wait-<name>`) built in
Go with one `poll` step, the way wake-ups are built today. That reuses the host poll
driver, releases the concurrency slot while parked, and makes a wait visible in
`cloche list` and stoppable with `cloche stop`. The run's end writes the inbox event.

`cloche dra ruling` and the "ruling" event kind are removed; `wait questions`
replaces them. DRAs are unreleased, so nothing depends on the command.

The DRA's daemon permissions gain: `questions ask`, `questions await`,
`questions read`, and registering, listing and cancelling its own waits.

## Part 8: The `saga` module, and removing Saga from the core

`saga-judge` becomes `saga`, a `qa` backend:

| Today (`saga-judge`) | After (`saga`) |
|---|---|
| `ask`: `saga_batch_file` → `saga_batch_id` | `ask`: `questions_file` → `question_ids` (still one `saga ask` batch inside) |
| `await-answer`: a blocking poll step, `saga_outcome` with `reframe` | `await-answer`: one check, `questions_open` |
| — | `read-answer`: `saga show`, human's ruling preferred |
| `judge` workflow, `judge-prepare.sh`, `prompts/judge.md` | removed; `judge.md` holds only what is specific to Saga |
| `SAGA_PROJECT` / `SAGA_FEATURE` from the daemon environment | `SAGA_PROJECT` on the bind; feature from the question file's `backend.feature` |

`judge.md` for Saga says to use `saga search --follow`, to answer with
`saga answer --judge` (which Saga refuses when a ruling exists), and that `saga hold`
is available.

Once DRAs ask through `cloche questions`:

- the `saga` binary comes out of the DRA image (`docker/cloche-dra/Dockerfile`),
- `CLOCHE_SAGA_ADDR` comes out of the locked container's allowlist
  (`internal/adapters/docker/locked.go`),
- the "ruling" event text in `internal/dra/triggers.go`, the last mention of Saga in
  the DRA code, goes with `cloche dra ruling`.

## Testing the design against other backends

Not built. This is the check that the contract is not Saga in disguise.

| Need | Saga | Notion page | Obsidian vault |
|---|---|---|---|
| ask | `saga ask` | append a numbered item to "Open Questions" | append to a markdown file |
| question id | Saga's id | the block id | a block anchor (`^q-7f3a`) |
| is it answered? | `saga poll` | the item has a child bullet | the item has a child bullet |
| who answered | judge author kind | the block's author (bot or person) | `git blame`, or a `Judge:` prefix |
| the record a judge searches | `saga search --follow` | Notion search; the answered items | grep the vault |
| history of judge decisions | Saga | page history | git log |
| "a judge looked, left it" | `saga hold` | a `Held:` bullet, if wanted | a `Held:` bullet, if wanted |

Where a page-based backend is weaker: a human may answer in a way the script cannot
parse (rewording the question, replying in a comment). `await-answer` should then
keep reporting the question as open, and the module's `asking.md` should state the
convention so that the page stays readable by both.

### Conformance test

One shared test, run against every backend module (with a stub for the tool): ask a
question; `await-answer` reports it open; answer it; `await-answer` reports none
open; `read-answer` returns the text; add a human answer beside a judge's and
`read-answer` returns the human's.

## What changes for users

- **New:** `cloche questions`; `bind <group> = <module>`; settings on a bind;
  `[questions] judge`; `cloche dra wait` and `cloche dra waits`; `judge.md` and
  `asking.md` in a module.
- **Renamed:** module `saga-judge` → `saga`; its keys lose the `saga_` prefix.
- **Removed:** the `judge` and blocking `await-answer` workflows of `saga-judge`; the
  `reframe` outcome; `cloche dra ruling`; Saga in the DRA image and allowlist.
- **Unchanged:** per-slot binds, the six-line tracker bind, poll steps.

Nothing binds `saga-judge` today and DRAs are unreleased, so no project needs
migrating.

## Build order

1. **Group binds and bind settings.** Parser, resolver, validation, docs. Everything
   else sits on these.
2. **The `qa` contract and `cloche questions`.** Slots, CLI, HTTP routes, the
   conformance test.
3. **The `saga` module.** Rewrite of `saga-judge` against the contract.
4. **The built-in judge.** Prompt, launch on ask, `[questions] judge`, budget gate.
5. **DRA waits.** The wait run, the `wait` event, DRA permissions, removal of
   `cloche dra ruling`.
6. **Saga out of the core.** DRA image and allowlist.

## Still open

Nothing is waiting on a Saga ruling. The last five were settled by Lucas there:

- CLOCHE-A149 (on CLOCHE-Q141): a DRA names a poll; it cannot supply the script
  (Part 7). It replaces CLOCHE-A144, the first answer to the same question.
- CLOCHE-A145: `await-answer` is a single non-blocking check (Part 1).
- CLOCHE-A146: the wait limits in Part 7 are a reasonable starting place.
- CLOCHE-A147: the earlier judge rulings (CLOCHE-A109 to A112, A127) are left as they
  are; they still describe the Saga backend.
- CLOCHE-A148 (on CLOCHE-Q145): a judge may rule on a new question, citing existing
  questions and answers or design documentation (Part 6). CLOCHE-A150, an agent
  answer added at Lucas's request, restates it and marks CLOCHE-A109 as amended, so
  the earlier ruling now points here.

Open, not asked in Saga:

- **Building a wait as a Go-built host run** assumes it behaves like a workflow
  parsed from a file. Wake-ups are built this way already, but this was not checked
  against the poll driver.
- **Custom poll scripts** live under `.cloche/dras/<name>/polls/`. Whether a
  project-wide location is also wanted is left until a second DRA needs the same
  poll.
- **`ready` on a DRA-created task** is still undecided (see `docs/dras.md`); this
  design does not change it.
