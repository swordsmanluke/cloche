# Arm overlays (E5)

Part of the [Intent A/B experiment](../../../../docs/plans/2026-09-14-intent-ab-experiment-protocol.md).
Per-arm file overlays applied on top of a clone of the seed repo
(`experiments/intent-ab/seed/`, E1) by `arm_driver` (`../arm_driver/`), plus
the 2-task stub list used for the driver's own smoke test.

## Layout

- `common/` — applied to **both** arms. Overrides `.cloche/develop.cloche`
  (adds the workflow-level `container{}` block pointing `agent_command` at
  the bonsai executor wrapper, E4) and `.cloche/Dockerfile` (drops the
  seed's Claude-Code install block, dead weight once nothing in either arm
  uses it). The executor is a controlled constant across arms — see the
  protocol doc's design table — so this lives outside the arm-specific
  directories.
- `arm-a/` — Arm A ("vanilla") overlay: `.cloche/config.toml` setting
  `intent.scan_after_tasks = false`. No `.cloche/intent/` directory is
  shipped or ever created — that absence is the point, and the driver
  asserts it holds throughout the run.
- `arm-b/` — Arm B ("intent") overlay: `.cloche/config.toml` setting
  `intent.token_budget = 1000`. Everything else (including
  `scan_after_tasks`) is left at its default.
- `stub-tasks.json` — a 2-task `bd create --graph` list (`stub-01` blocked
  on `stub-02`... — `stub-02` blocked on `stub-01`) used only by the
  driver's own end-to-end smoke test (E5's acceptance bar). It is not part
  of the frozen 12-task Bract build list (`seed/.cloche/tasks/seed-tasks.json`,
  E2) and must never be used for an actual pilot/replication run.

## Composition order

For a target arm, `arm_driver.overlay.apply_overlay` copies, in order (later
wins on file-path collision):

1. The cloned seed tree (from `seed-v1`, or whatever ref is given).
2. `harness/agent_command/` and `harness/bin/` (E4's wrapper source, copied
   live so the arms never carry a stale duplicate of it).
3. `common/`.
4. `arm-a/` or `arm-b/`.

## Contamination invariants

Enforced by `arm_driver.contamination` at every poll tick, not just at
setup, because a scan could in principle be triggered mid-run:

- Arm A's project tree never contains a `.cloche/intent/` directory.
- Neither arm's project tree ever contains the hidden eval corpus
  (`experiments/intent-ab/eval/`, E3) — checked by directory name and by
  scanning for any of the corpus's `.bract` filenames anywhere in the tree.
