# Design: Human-in-the-Loop Review / Test Cycle

## Original Feedback

> I believe cloche would benefit tremendously from having some bespoke
> features to manage a review/test cycle that involves a human in
> the loop at certain points. I'm not sure what that would look like.


# Design

## Overview

A `human` step type is a special workflow step whose script is polled repeatedly by the
orchestrator until a human decision is available. It allows a workflow to pause and wait
for external human input (e.g. a code review, an approval gate, a ticket transition)
without holding a blocking process open.

## Step Semantics

The `human` step runs a script on the host. Exit code and stdout wire output are
interpreted as follows:

| Exit code | Wire output | Meaning |
|-----------|-------------|---------|
| 0         | none        | **Pending** — script ran successfully, no human decision yet. Orchestrator will poll again later. |
| non-zero  | none        | **Failure** — follows the `fail` wire. The user is responsible for wiring `fail` appropriately (abort, retry, notify, etc.). |
| any       | wire name   | **Decision** — follow the named wire to the next step, same as any other step type. |

The only behavioural difference from a regular script step is that exit 0 + no wire
means "keep polling" rather than "done."

## Polling

The orchestrator polls the script at a fixed interval declared on the step. There is no
backoff — the interval is constant for the lifetime of the step.

```
step "code-review" {
  type     = human
  script   = "scripts/check-pr-review.sh"
  interval = "5m"
}
```

The `interval` field is required for `human` steps.

## Timeout

All step types support an optional `timeout` field. If the step does not complete within
the timeout duration, the orchestrator follows the `timeout` wire. If no `timeout` wire
is declared, it implicitly binds to `abort`.

The default timeout for `human` steps is **72h**. Other step types retain whatever
default is already defined for them.

```
step "code-review" {
  type     = human
  script   = "scripts/check-pr-review.sh"
  interval = "5m"
  timeout  = "48h"
}

code-review:timeout   -> escalate
code-review:approved  -> merge
code-review:fix       -> address-feedback
```

## Execution Environment

`human` steps can run on the host or inside a container, following the same rules as
other step types. Most polling scripts are lightweight enough to run directly on the host.
Container execution is available for cases where isolation is desirable — for example, a
script that uses an LLM agent to scan an email inbox for a specific response should be
containerized to avoid running an agent in an unrestricted context on the host, especially
when the input may come from unknown senders.

When a container is specified, the orchestrator reuses the existing named container across
poll invocations rather than spinning up a new one each time. Host scripts use
`cloche get/set` for KV access; container scripts use `clo get/set`.

## Passing Context to the Polling Script

The polling script accesses run state via the KV store. Container steps write values with
`clo set <key> <value>`; host-side scripts read them with `cloche get <key>`. The daemon
persists KV data for the lifetime of the run, so values written by earlier steps are
available when the `human` step polls.

It is the user's responsibility to ensure the relevant keys are set before the `human`
step is reached.

```bash
# In a container step (e.g. create-pr)
clo set pr-id 1234

# In the human polling script (host-side)
PR_ID=$(cloche get pr-id)
```

## Orchestration and State

The orchestration loop drives human step polling via a `PollCoordinator`. When an
executor reaches a `human` step, it registers a session with the coordinator and blocks
on a result channel. On each loop tick, the coordinator's `DrivePolls` is called: sessions
whose `now >= lastPollAt + interval` have their script invoked in a background goroutine.
When the script returns a decision (non-empty wire name), the result is sent on the
channel and the executor unblocks.

The `interval` is a "no sooner than" constraint — the orchestrator does not guarantee
exact timing. The loop must tick frequently enough that the actual trigger time is within
~30 seconds of the ideal time.

Poll state (`last_poll_at`, poll count) is stored per `(run_id, step_name)` in
`HumanPollStore` for observability (e.g. `cloche status`). The coordinator tracks
`lastPollAt` in memory; the DB record is updated after each pending result. No separate
scheduler or timer management is required.

`cloche list` and `cloche status` should surface runs waiting at a `human` step
distinctly — e.g. a `waiting` status alongside the step name and time since last poll.

## Script Idempotency

The polling script is invoked once per interval for the entire duration of the step. It
is the user's responsibility to ensure the script is idempotent — side effects such as
posting a GitHub comment, sending a notification, or writing to an external system must
not be triggered on every poll invocation.

## Overlapping Invocations

If a poll invocation is still running when the next interval is due, the orchestrator
skips that poll and tries again at the following interval. If the invocation has been
running for longer than 4× the configured interval (i.e. three consecutive skips), the
orchestrator fails the step with an explanatory log message and follows the `fail` wire.

## Transient Script Failures

A non-zero exit with no wire output is treated as an immediate `fail` wire trigger, the
same as any other step failure. Retry logic for transient errors (e.g. flaky network
calls) can be handled within the script itself (return exit 0 to stay pending) or via the
existing workflow retry mechanisms.

