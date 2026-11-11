# saga-judge

Ask questions in [Saga](the decision-record tool) from a workflow, wait for the answer,
and let a judge agent settle the questions that an existing ruling already decides.
Design: `docs/plans/2026-11-10-autonomous-loop-design.md`, Part 2 (rulings CLOCHE-A109,
A110, A111). See `docs/modules.md`.

## Provides

| Workflow | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `ask` | `saga_batch_file` | `saga_batch_id` | `saga ask -p <project> -f <feature> --file <file> --json`; stores the batch id read from the JSON output. Fails with a clear message if project or feature is missing. |
| `await-answer` | `saga_batch_id` | `saga_outcome` | A poll step (every 1m) running `saga poll <batch> --no-wait --json` (exit 0 = all answered, 2 = still open and not an error, 1 = failure; reads `.done` and `.questions[].status.state`). `answered`: every question has a ruling, whoever gave it (ends `done`). `reframe`: any question was deprecated by the judge (`saga_outcome = reframe`, ends `abort`). `timeout`: ends `abort` with `saga_outcome` unset. |
| `judge` | `saga_batch_id` | — | A host agent step. For each open question in the batch it runs `saga search --follow`, reads the intent entries, then decides (`saga answer --judge`, citing the ruling or intent id), sends back (`saga answer --judge --deprecates <id>`, saying why) or holds (`saga hold --reason`, leaves it open). Exit 3 from answer/hold (already ruled) is fine. Permission requests are in scope; it holds unless certain the change is safe and valid. It never answers from its own reasoning alone. It always ends `done`: a failed judge just leaves the questions to the human. |

`example-ask-with-judge` (private) wires `ask -> await-answer` and `ask -> judge` in
parallel and joins them. Copy it into a project workflow and bind the three slots to
`saga-judge`.

Only questions asked by a workflow are judged; questions from an interactive session go
straight to the human.

## Inputs and configuration

- `saga_batch_file`: path to the batch JSON file for `saga ask`:
  `{"title","context","questions":[{"prompt","detail","options":[{"label","description","recommended"}]}]}`.
- `SAGA_PROJECT` / `SAGA_FEATURE` (or kv `saga_project` / `saga_feature`): required by `saga ask`.
- `SAGA_BIN`: the saga binary (default `saga`). Saga's server is `--server` or `$SAGA_URL`
  (default `http://127.0.0.1:7878`).

`saga poll <batch> --no-wait --json` prints
`{"done":bool,"questions":[{"id","batch","type","prompt","answers":[…],"status":{"state":"open|held|in-force|deprecated|…"}}]}`;
`poll.sh` and `judge-prepare.sh` read that shape. `--json` is required wherever output is
parsed; without it saga prints markdown.

## Prerequisites

- `saga` and `jq` on the host `PATH`, with Saga reachable from the daemon host.
- A Saga with the judge author kind, the held mark and refuse-if-answered
  (tracked in Saga's own tracker). Without refuse-if-answered a judge answer can
  override a human's.
- `cloche` on the host `PATH`. The judge runs as a host agent step; no container image
  is involved.

## Credentials

None in cloche; `saga` uses whatever configuration it already has.
