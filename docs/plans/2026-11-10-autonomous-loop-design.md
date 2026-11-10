# Autonomous Loop: Budget Caps, Judges, Review, Feedback Intake and DRAs

**Date:** 2026-11-10
**Status:** Draft. Rulings from Saga batch CLOCHE-B23 are folded in and cited by
answer id (`CLOCHE-Ann`), with CLOCHE-A122 and CLOCHE-A123 from the two follow-up
batches. What remains is listed under "Still open" at the end.
**Decision record:** Saga feature `cloche/autonomous-loop`.
**Prompted by:** Spawn's write-up of running a company on a fleet of agents (`spawn.md`,
not checked in).

## Problem

Cloche runs a work loop: `cloche loop` takes tasks from a tracker, runs each in a
container through implement → test → fix → docs, and merges to `main`. Saga records
requirements as questions and rulings. Intent injects standing requirements into
prompts.

Three things still wait on a human, and they set the ceiling on how much the loop
can do unattended:

1. **Every question waits for Lucas.** A run that asks in Saga parks until he
   answers, even when he has answered the same thing before. The repairman has asked
   "May the repairman change these files?" four times (CLOCHE-Q61, CLOCHE-Q65,
   CLOCHE-Q66, CLOCHE-Q102) and the answer was "Allow" every time.
2. **Every task is written by a human.** Nothing turns a failed run, a red test
   suite or an error in a running app into a task.
3. **Nobody watches `main` after a merge, or the spend.** Each run is checked before
   it merges. Nothing checks `main` afterwards. Token burn is reported in
   `cloche status` but nothing acts on it. The only limit is the per-run
   `token-limit`, which does nothing about 500 runs that each stay under it.

## Goals

- Unattended running can't drain a subscription's usage allowance.
- A question that has already been answered on the record gets that answer without
  a human, with a citation, and the human can overrule it.
- Failures and metrics from running systems become tasks without a human writing
  them.
- `main` is checked regularly, and a red `main` gets fixed by a run.
- A change is attacked by an agent that didn't write it before it merges.
- A long-lived, named agent can manage a domain's work the way an interactive
  session does today.

## Non-goals

- Dropping the pre-merge gate. Spawn merges first and fixes after. We keep
  implement → test → merge and add the post-merge check on top.
- Deploying anything. Deploy, health checks and rollback are a project's own
  feedback source (Part 4), not core.
- Agents coordinating by messaging each other. Coordination stays in shared state:
  the repo, the tracker and Saga.
- Replacing human rulings on new questions. A judge only applies what's on record.

## Overview

Five parts, in build order (CLOCHE-A105). Each one is useful without the later ones.

| Part | What | Where the work is |
|---|---|---|
| 1 | Budget caps | cloche daemon |
| 2 | Judge | a cloche workflow, small Saga changes |
| 3 | Review loop | workflow files only |
| 4 | Feedback intake, including a regular check of `main` | cloche daemon |
| 5 | DRAs (Domain Responsible Agents) | cloche daemon, a container image |

Budget caps come first because every later part increases spend.

## Part 1: Budget caps

### What exists

- Token usage is recorded per step and summed per agent. `cloche status` and the
  dashboard show tokens per hour over the last hour.
- `token-limit` on a workflow or step aborts that one run.

### The unit: a share of the usage allowance per day

The cap is a percentage of the subscription's usage allowance that cloche may use
per day (CLOCHE-A106): the same thing Claude Code's `/usage` measures. With a
weekly allowance:

| Cap per day | What it means |
|---|---|
| 20% | Used up in 5 days. Fine for a work week, nothing left for the weekend. |
| 1/7 (about 14%) | Runs out just as the allowance resets, if nothing else uses it. |
| 10% | Leaves headroom for unplanned and emergency use. |

How cloche measures that share, in order of preference:

1. **Read it.** If the figure behind `/usage` can be read by a program, the daemon
   reads it and the cap is exact. Whether it can is not known yet; that's the
   first task of this part.
2. **Estimate it.** Otherwise the daemon estimates from what it already counts:
   tokens times a per-model multiplier, against a configured size for the weekly
   allowance. Models use up allowance at different rates, so the multiplier is per
   model. The estimate is refined over time: whenever a real reading is available
   (even one typed in from `/usage`), the daemon corrects the allowance size.
   Usage records hold the agent's name but not the model today
   (`domain.TokenUsage`), so the model has to be recorded per step first.

The cap applies per agent provider. A Claude allowance and a Codex allowance are
separate, so each gets its own figure; the first version covers Claude only.

### Configuration

One cap for the whole daemon, not per project (CLOCHE-A108):

```toml
# daemon config
[budget]
usage_percent_per_day = 10
grace = "30m"          # how long runs in flight may continue once the cap is hit

# only used when the allowance can't be read:
weekly_allowance_tokens = 500_000_000      # starting estimate, refined over time
[budget.model_weight]
opus   = 5.0
sonnet = 1.0
haiku  = 0.3
```

The numbers above are placeholders, not measured values.

### When the cap is hit

(CLOCHE-A107)

- **No new runs launch.** That covers loop tasks, timed work (Part 4) and
  DRA wake-ups (Part 5). `cloche run` by hand still works, with a
  warning.
- **Runs in flight get `grace` to finish.** After that they are stopped, to limit
  the overrun. Stopping parks the run (the container is committed and stopped, as
  parked runs are today), so the work isn't lost and `cloche resume` continues it.
- **It shows up.** `cloche status` gets a `Budget:` line, and "Needs you" gets a
  `budget` kind that names the cap and when the day's window clears.
- **Launching resumes on its own** when the day's usage is back under the cap.

A spike alert is a second, lower threshold: if the last 15 minutes ran at more than
N times the previous day's average rate, flag it in "Needs you" without pausing.

## Part 2: Judge

### Flow

A workflow that asks in Saga fans out to two steps. Fan-out and `poll` both exist
today; a run parked at a `poll` gives up its concurrency slot. Only questions asked
by a workflow are judged; questions from an interactive session go straight to
the human (CLOCHE-A111).

```
step ask          { run  = "scripts/ask.sh" ... }          # saga ask, stores batch id
step await-answer { poll = "scripts/saga-poll.sh" ... }     # saga poll --no-wait
step judge        { prompt = file("prompts/judge.md") ... } # host agent step

ask:success -> await-answer
ask:success -> judge
```

The judge reads each open question in the batch and does one of three things:

| Outcome | When | What it does in Saga |
|---|---|---|
| **Decide** | A Saga ruling in force, or an intent entry, settles the question (CLOCHE-A109) | `saga answer` with the ruling, citing the source by id |
| **Send back** | The question is wrong: it rests on a false premise, or was already answered under another question | `saga answer --deprecates` on the question, saying why |
| **Hold** | It needs judgment nobody has given, or the judge isn't sure | Marks it held, leaves it open for the human |

`await-answer` resolves when every question has a ruling, whoever gave it. The poll
script reports `answered`, or `reframe` when any question was sent back, so the
workflow can wire a re-ask.

The judge is a host agent step (like `changelog`'s `draft`), because it needs the
Saga server and the intent files, and no workspace.

The ask, poll and judge steps ship as a module (`saga-judge`), so any project's
workflow can use them without copying scripts.

### Permission requests

Permission requests are in scope (CLOCHE-A110). Deciding them is what a judge is
for. For a request like the repairman's "May the repairman change these files?",
the judge looks at earlier rulings on the same kind of request and at what the
change is. If it isn't certain the change is safe and valid, it holds the question
for the human.

### The learning loop

When the judge is wrong, the human answers the same question with `--supersedes`
on the judge's ruling. `saga search` then shows the judge's ruling as superseded
and names the human's. The judge prompt requires `saga search --follow`, so the
next judge reads the correction. Nothing new is needed for this.

### Checking the judge: a daily digest

(CLOCHE-A112) Once a day, a summary of what the judges decided automatically: each
question, the ruling, and the source cited. Questions held for a human aren't in
it; they are already listed as open on Saga's landing page.

### Changes to Saga

- **Authorship.** Saga records an answer as written by a human or an agent. A
  judge needs its own kind, so its rulings can be listed for the digest and told
  apart from answers an agent recorded on the human's behalf.
- **A held mark.** "The judge looked and declined" is different from "nobody has
  looked". The mark stops a second judge re-reading the question.
- **Answer only if still open.** The newest answer is the ruling in force. If the
  human answers while the judge is still working, the judge's later answer would
  override the human's. So a judge's answer must be refused when the question
  already has a ruling.
- **The digest.** A query for judge rulings since a given time. The digest itself
  can be a cloche workflow that formats the result.

## Part 3: Review loop

A step in `develop.cloche`, after `test` passes and before `update-docs`:

```
step review {
  prompt       = file(".cloche/prompts/review.md")
  max_attempts = 2
  results      = [success, fail, give-up]
}

test:success   -> review
review:success -> update-docs
review:fail    -> fix          # fix:success -> test, as today
review:give-up -> update-docs  # out of attempts: go on, findings stay in the run
```

The review prompt gets the diff against the base commit and the task, and is told
to find a failing case: write a test that fails, or name the input that breaks it.
It reports `fail` only with evidence, and writes its findings where `fix` reads
them. `max_attempts` caps the loop: once it's used up the step reports `give-up`,
and the change goes on with the findings recorded in the run.

This needs no change to cloche. Which agent or model reviews, and what context it
gets, is a per-step choice the workflow DSL already allows (CLOCHE-A113). The step
goes into this repo's and manager's workflows first.

## Part 4: Feedback intake

Signals from failed runs, from a regular check of `main`, and from a deployed app
are the same thing: a source produces an event, something decides whether it
matters, and the ones that matter become tasks with the evidence attached.

Cloche **accepts** signals through an API; it doesn't go and fetch them
(CLOCHE-A121). Receiving sits outside the task loop. Whoever owns a source writes
a small adapter that turns its events into cloche's signal format. Two alternatives
were considered and fall out of this one instead of being built:

- **A pull loop owned by cloche.** A source that has to be polled is a workflow on
  a timer (see "Pull adapters and timing") whose script checks the source and
  sends a signal.
- **No mechanism: sources create P0 tasks directly.** This keeps working, since
  the loop takes the highest-priority task anyway. But the daemon only finds out
  at the next `list-tasks`, so it can't de-duplicate or react.

The name is "signal", not "notify": cloche's notifier port and the help-channel
design use "notify" for messages going out to the user.

### The signal format

| Field | Required | Meaning |
|---|---|---|
| `source` | yes | Who is reporting: `ci-main`, `manager-app`, `sentry` |
| `key` | yes | The caller's uniqueness key: two signals with the same `source` and `key` are the same problem |
| `severity` | yes | `low`, `high` or `urgent` |
| `title` | yes | One line |
| `evidence` | no | Text: log lines, failing tests, a metric's values |
| `domain` | no | A DRA's domain (Part 5), when the source knows it |

**The caller chooses the key** (CLOCHE-A116). Cloche never works out what
"the same problem" means for a source; it only compares keys. A source that
de-duplicates already passes its own id through (a Sentry issue id, a DataDog
monitor id). A source that doesn't builds one in its adapter (a test name, an
error class plus top stack frame, a metric name).

### Sending one

From a script on the same machine:

```
cloche signal --source ci-main --key "main-red" --severity urgent \
  --title "main is red: 3 tests fail in internal/host" --evidence-file out.txt
```

From anything else on the network, the same fields as JSON:

```
POST /api/projects/{name}/signals
Authorization: Bearer <token>
```

The endpoint is on the dashboard's HTTP listener. It needs a per-project token,
because a signal can create work that spends budget.

### What the daemon does with a signal

The daemon stores every signal, then does one of four things. It tries the
deterministic ones first and uses an agent only for what's left.

1. **Attach.** The source and key match an open task: add the occurrence to that
   task and stop. One recurring error makes one task, not fifty.
2. **Drop.** The key is muted, or the signal is below its source's threshold (for
   example, fewer than 3 occurrences in an hour for `low`): keep the record and
   stop.
3. **Act directly.** Some signals change what the daemon itself does: an `urgent`
   signal may launch over the concurrency limit (see "Emergency launches"), and a
   signal with a `domain`
   wakes that domain's DRA.
4. **Create a task.** A small-model step reads the signal and writes the task: a
   title, a body with the evidence, a priority from the severity. It may instead
   say "noise", which mutes the key.

Work that needs code changed always ends as a task in the tracker. There is one
queue of work, and all of it goes through the budget, the tests and the review
step.

To create the task, the daemon needs a way into the tracker. The proposal is a
`create-task` phase in the module contract, next to `list-tasks`, `claim-task`,
`close-task` and `release-task`, so the signal API works with whichever tracker
`project.cloche` binds. This detail hasn't been ruled on.

### A red `main`: fix forward

Nothing pauses when `main` goes red (CLOCHE-A115). Pausing merges could block the
fix itself. Runs keep merging, and the fix lands on top of whatever else landed.
The design optimises for that:

- the red-`main` signal is `urgent`, so its task goes first
- the task carries the failing tests and the commit range since the last green
  check
- while the task is open, later red checks attach to it with a wider commit range
  instead of filing new tasks

### Emergency launches

Runs in flight are never killed or paused for a signal: that throws away what
they've spent. Without anything else, an urgent task would wait for a free
concurrency slot, which can take as long as an `implement` step's 90-minute
timeout. So urgent tasks may launch over the limit, up to a separate limit of
their own (CLOCHE-A122):

```toml
[orchestration]
concurrency = 5   # how many tasks may be in flight
emergency   = 1   # how many urgent tasks may launch above that
```

- A task made from an `urgent` signal launches at once while fewer than
  `concurrency + emergency` tasks are in flight.
- Emergency tasks count against `concurrency`. With 5 ordinary tasks and 1
  emergency task in flight, the total is 6, so no new ordinary task starts until
  the total is back under 5.
- `emergency = 0` turns it off: urgent tasks go to the front of the queue and
  wait for a slot.
- A budget cap still stops emergency launches.

### Pull adapters and timing

A source the daemon can't hear from has to be pulled: a workflow runs on a timer,
its script asks the source what's new, and it sends signals.

Cloche has no time-based trigger, and none is decided yet (CLOCHE-A114): more
needs to be known about the feedback sources first. Until then a timer is built
from what exists: a workflow that loops through a `poll` step whose script
compares the current time with a target timestamp. It is approximate, which is
enough for these cases.

### Cases the design supports

(CLOCHE-A116) These are cases to support, not a build list. For each: how the
signal reaches cloche, and where its key comes from.

| Case | How it arrives | Key |
|---|---|---|
| Failed and aborted cloche runs | The daemon signals itself when a task reaches the repeat-failure threshold (the point where "Needs you" flags it today). One failure isn't a signal: the task is still open and the loop retries it. The new task is "find out why this task keeps failing" | task id plus failed step |
| Regular check of `main` | A timer workflow runs the full suite on `main`'s HEAD in a container and calls `cloche signal` on red | fixed: `main-red` |
| Daemon errors in cloche's own logs | The daemon signals itself from its error path | error site (file and function) |
| manager: app errors | An adapter in manager reads its logs and calls `cloche signal` | error class plus top stack frame |
| anarkana: remote metrics | The remote system can't post to a developer's machine, so a pull adapter fetches the metrics on a timer | metric name plus threshold crossed |
| DataDog error logs | Pull adapter: query DataDog's API for monitors in alert | DataDog monitor id plus group |
| Sentry alerts | Pull adapter: query Sentry's API for new and regressed issues | Sentry issue id |

DataDog and Sentry can also push webhooks, in their own formats and from the
internet. A daemon on a developer's machine isn't reachable from there, so pull is
the default. Where the daemon is reachable, a small relay that reshapes the
webhook into the signal format and posts it to the HTTP endpoint does the same job.
Cloche itself only ever accepts its own format.

## Part 5: DRAs (Domain Responsible Agents)

A DRA is the agent that owns responsibility for one domain of the software
(CLOCHE-A123). It is a dedicated, named agent session with a long memory
(CLOCHE-A117). It manages its domain's work: it reads what came in, files and
orders tasks, asks questions in Saga, and reads results. It works the way an
interactive session with Lucas works today, one layer up from runs. It is a new
concept in cloche.

A domain is whatever the user defines it to be. It can be a section of the code,
such as authentication, UI design or the engine. It can also cut across the code,
such as performance or reliability.

### Defined by the user, zero or more

(CLOCHE-A120) A project has as many DRAs as its user defines. Zero is
the default: a project that sticks to ordinary workflows has none. A user adds one
each time they hand off a portion of responsibility.

```
.cloche/dras/<name>/
  charter.md   # the domain, what it may do, the skills it uses
  notes.md     # its memory: commitments, what it tried, what it's waiting on
```

### Runs in a locked-down container

(CLOCHE-A118) A long-lived agent is the riskiest kind: over a long life it is the
most likely to lose track of what it was meant to do. So it runs in a container,
not on the host, with tighter limits than a workflow run:

- **Its own image.** A Dockerfile for DRAs, separate from the project's
  build image. It holds `cloche`, the tracker's CLI and `saga`, and no build tools.
- **Network.** The allowlist is the model API plus the daemon and Saga on the
  host. Containers already reach the daemon's gRPC port (that's how `clo` and
  `cloche-agent` work), but only for a run's own key-value store and step
  results. A DRA needs more: listing tasks and runs, reading run output, creating
  tasks. Those calls need to be limited to what a DRA may do, so a DRA can't, for
  example, shut the daemon down or delete runs. Saga's port has to be added to the
  allowlist. How a DRA reaches the tracker is open.
- **Workspace.** The project is mounted read-only, apart from the agent's own
  `notes.md`.

### How it lives

A wake-up is an ordinary container workflow run with one prompt step. The prompt
adapter can already resume a named session. What's new is keeping the session's
files between runs (a volume per DRA), the DRA image, and the wake-up triggers.

- **The session is disposable; the notes are the memory.** A DRA has a
  named Claude Code session, started with `--session-id` and continued with
  `--resume`. The daemon keeps the id. A long session gets compacted and will
  eventually be lost, so the agent reads `notes.md` when it wakes and updates it
  before it stops. If the session is gone, a new one starts from the charter and
  the notes.
- **It isn't a running process.** The daemon starts the container with a message
  that says what happened, and the agent runs until it has nothing left to do.
  Wake-ups:
  - a task in its domain finished, failed or parked
  - a signal arrived for its domain
  - a ruling arrived on a question it asked
  - a heartbeat, for example every few hours
- **Domains are labels.** A task carries a domain label, and belongs to the DRA
  of that domain. A signal names its domain, or the step that writes the task
  picks one from the charters. A task that fits two domains, such as an engine
  change made for performance, gets one label; the other DRA isn't woken.
- **It manages; runs do the work.** (CLOCHE-A119) Within its domain it creates,
  orders, comments on and closes tasks, asks and reads Saga, reads run output,
  and writes its notes. It doesn't stop or delete runs, and it never changes
  code. Code changes go through tasks and runs, so they keep the container, the
  tests and the review step.
- **Wake-ups are budgeted.** Each wake-up is recorded as a run with token usage,
  and Part 1 applies.

## Build order

(CLOCHE-A105)

1. **Budget caps.** First task: find out whether the usage allowance can be read.
   Then the cap, the grace period and the status line.
2. **Judge.** One workflow, a prompt, the digest, and the Saga changes.
3. **Review loop.** Workflow files only.
4. **Feedback intake.** `cloche signal` and its HTTP endpoint, the daemon's
   handling of a signal, task creation, then the check of `main` and the
   failed-run source.
5. **DRAs.** Last, because one with no signals, no judge and no budget
   has nothing to manage and nothing limiting it.

## Still open

With Lucas in Saga, batch CLOCHE-B26:

- **CLOCHE-Q121:** whether the daily percentage counts cloche's usage only or the
  whole account's.
- **CLOCHE-Q122:** whether to cap the short usage window too.
- **CLOCHE-Q123:** whether an emergency task may launch past the cap. The doc
  says no until ruled.
- **CLOCHE-Q124:** where the judge digest appears.
- **CLOCHE-Q125:** how signals and DRAs reach the tracker. The doc proposes new
  module phases.
- **CLOCHE-Q126:** how small the first version of signals is. Part 4 describes
  the full version.
- **CLOCHE-Q127:** where a DRA's notes live.

Not for Saga:

- **Time triggers** (CLOCHE-A114): deferred until the feedback sources are better
  understood. A `poll`-step timer is used meanwhile.
- **Reading the usage allowance:** unknown until the Part 1 spike.
