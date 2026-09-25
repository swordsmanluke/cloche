# Arm overlays (E5)

Part of the [Intent A/B experiment](../../../../docs/plans/2026-09-14-intent-ab-experiment-protocol.md).
Per-arm file overlays applied on top of a clone of the seed repo
(`experiments/intent-ab/seed/`, E1) by `arm_driver` (`../arm_driver/`), plus
the 2-task stub list used for the driver's own smoke test.

## Layout

- `common/` — applied to **both** arms. Overrides `.cloche/develop.cloche`
  (adds the workflow-level `container{}` block pinning every prompt step to
  the executor, `claude-haiku-4-5` via Cloche's native `claude` adapter) and
  `.cloche/Dockerfile` (the seed's Dockerfile plus a Claude Code install that
  actually builds on this base image — NodeSource, not Debian's nodejs). The
  executor is a controlled constant across arms — see the protocol doc's
  design table and its executor amendment — so this lives outside the
  arm-specific directories.
- `arm-a/` — Arm A ("vanilla") overlay: `.cloche/config.toml` setting
  `intent.scan_after_tasks = false`. No `.cloche/intent/` directory is
  shipped or ever created — that absence is the point, and the driver
  asserts it holds throughout the run.
- `arm-b/` — Arm B ("intent") overlay: `.cloche/config.toml` setting
  `intent.token_budget = 1000`. Everything else (including
  `scan_after_tasks`) is left at its default.
- Both arm configs also set `[orchestration] concurrency = 1` and
  `max_consecutive_failures = 12`: one task in flight per arm, and retries
  of a hard task are capped per task by the driver (an execution outcome)
  rather than by the daemon halting the loop.
- `stub-tasks.json` — a 2-task list (`stub-01` blocked on `stub-02`... —
  `stub-02` blocked on `stub-01`) used only by the driver's own end-to-end
  smoke test (E5's acceptance bar). It is not part of the frozen 12-task
  Bract build list (`seed/.cloche/tasks/seed-tasks.json`, E2) and must never
  be used for an actual pilot/replication run.

## Why the overlay files carry no comments

The intent scan mines the arm's commit history. In the x4 replications, the
explanatory comments in these files were extracted into four
"requirements" about experiment infrastructure (executor choice, the
Dockerfile install method, the `prompt.txt` path, the develop wiring). So the
overlay files are deliberately bare, the overlay is folded into the seed's
initial commit rather than committed separately, and the rationale lives
here instead:

- `common/.cloche/develop.cloche` — `implement:fail -> commit -> test`: the
  agent's self-reported result marker is recorded by the driver's monitor but
  never gates the pipeline; the test suite does. Round one's dominant
  "failure" was a finished task followed by a bare `CLOCHE_RESULT:success`
  that the nonce classifier correctly rejected. `fix-tests` is bounded by
  `max_attempts` → `give-up` → abort.
- `common/.cloche/Dockerfile` — Claude Code is installed via NodeSource;
  Debian bookworm's nodejs/npm packages have broken interdependencies on the
  base image ("held broken packages"). The version is pinned (`2.1.281`, the
  one x5 ran on) so a replication built on a later day runs the same
  executor harness as the runs it is compared with.
- `common/.cloche/scripts/prepare-prompt.py` — also writes the task prompt to
  `.cloche/runs/<task-id>/prompt.txt`. The daemon builds each step's intent
  retrieval query from the text at that path, which only `cloche run --prompt`
  writes; without it every tracker-dispatched step queried on
  `"implement develop"` and arm B injected the same four generic
  requirements into every task (`cloche-hbcv`).
- `arm-*/.cloche/config.toml` — `concurrency = 1` (the task list is a
  chain); `max_consecutive_failures = 12` so retries are capped per task by
  the driver, not by the daemon halting the loop.

## Composition order

For a target arm, `arm_driver.overlay.apply_overlay` copies, in order (later
wins on file-path collision):

1. The cloned seed tree (from the frozen seed ref — `seed-v2 = <sha>` in the
   protocol doc; `seed-v1` was a tag, which Cloche does not carry through
   container extraction, so later freezes are recorded as commit SHAs).
2. `common/`.
3. `arm-a/` or `arm-b/`.

The E4 bonsai wrapper (`harness/agent_command/`, `harness/bin/`) is no
longer copied into arms: bonsai-8b-16k was dropped as executor after failing
pre-scoring in three harnesses (see the round-one results doc).

## Contamination invariants

Enforced by `arm_driver.contamination` at every poll tick, not just at
setup, because a scan could in principle be triggered mid-run:

- Arm A's project tree never contains a `.cloche/intent/` directory.
- Neither arm's project tree ever contains the hidden eval corpus
  (`experiments/intent-ab/eval/`, E3) — checked by directory name and by
  scanning for any of the corpus's `.bract` filenames anywhere in the tree.

## Replications and monitors (E8)

`python3 -m arm_driver --replications 3 --ref <sha> --target-dir <base>`
runs N fresh arm-A/arm-B pairs (`<base>/arm-a-r1`, `<base>/arm-b-r1`, …),
arms concurrent within a replication, replications sequential, and scores
each finished arm with `eval/runner.py` and `eval/audit/audit.py` into
`<base>/results/`. `arm_driver/monitors.py` classifies what the poll loop
sees: infrastructure failures (daemon unreachable, agent auth, image build,
extraction branch missing, loop halted for a container problem, no progress
for `--stall-seconds`) abort the arm, its sibling, and the run; execution
outcomes (marker drops, step failures, a task hitting
`--max-attempts-per-task`) are recorded and the next replication proceeds.
The known stale-slot bug gets one logged loop restart before it aborts.
