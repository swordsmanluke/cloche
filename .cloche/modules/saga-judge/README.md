# saga-judge

Ask questions in [Saga](the decision-record tool) from a workflow, wait for the answer,
and let a judge agent settle the questions that an existing ruling already decides.
Design: `docs/plans/2026-11-10-autonomous-loop-design.md`, Part 2 (rulings CLOCHE-A109,
A110, A111). See `docs/modules.md`.

## Provides

| Workflow | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `ask` | `saga_batch_file` | `saga_batch_id` | `saga ask <file>`; stores the batch id (the last `…-B<n>` token on stdout; override with `SAGA_BATCH_ID_PATTERN`). |
| `await-answer` | `saga_batch_id` | `saga_outcome` | A poll step (every 1m) running `saga poll --no-wait`. `answered`: every question has a ruling, whoever gave it (ends `done`). `reframe`: any question was deprecated by the judge (`saga_outcome = reframe`, ends `abort`). `timeout`: ends `abort` with `saga_outcome` unset. |
| `judge` | `saga_batch_id` | — | A host agent step. For each open question in the batch it runs `saga search --follow`, reads the intent entries, then decides (answers citing the ruling or intent id), sends back (`saga answer --deprecates`, saying why) or holds (marks it held, leaves it open). Permission requests are in scope; it holds unless certain the change is safe and valid. It never answers from its own reasoning alone. It always ends `done`: a failed judge just leaves the questions to the human. |

`example-ask-with-judge` (private) wires `ask -> await-answer` and `ask -> judge` in
parallel and joins them. Copy it into a project workflow and bind the three slots to
`saga-judge`.

Only questions asked by a workflow are judged; questions from an interactive session go
straight to the human.

## Inputs and configuration

- `saga_batch_file`: path to the batch file for `saga ask`.
- `SAGA_BIN`: the saga binary (default `saga`).
- `SAGA_BATCH_ID_PATTERN`: extended regex that finds the batch id in `saga ask` output.

`saga poll --no-wait <batch>` is expected to print JSON
`{"questions":[{"id":"…","status":"open|held|answered|deprecated"}]}`; the scripts use
that shape. Adjust `scripts/poll.sh` and `scripts/judge-prepare.sh` if Saga's output
differs. The judge prompt (`prompts/judge.md`) names `saga answer`, `saga answer
--deprecates` and "mark held" in prose; confirm the held-mark command against Saga.

## Prerequisites

- `saga` and `jq` on the host `PATH`, with Saga reachable from the daemon host.
- A Saga with the judge author kind, the held mark and refuse-if-answered
  (tracked in Saga's own tracker). Without refuse-if-answered a judge answer can
  override a human's.
- `cloche` on the host `PATH`. The judge runs as a host agent step; no container image
  is involved.

## Credentials

None in cloche; `saga` uses whatever configuration it already has.
