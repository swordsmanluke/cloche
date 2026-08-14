# 3. Run the Loop

The orchestration loop is the autonomous mode: the daemon finds ready tasks
and runs the whole pipeline for each one, unattended.

## Start it

```bash
cloche loop            # start; --max <n> caps concurrent runs
cloche loop once       # run just the next ready task, then stop dispatching
```

The loop is two-phase, matching the two workflows in `.cloche/host.cloche`:

1. **`list-tasks`** is polled — it prints ready tasks as JSONL.
2. For each dispatched task, **`main`** runs with `CLOCHE_TASK_ID` set in
   every step's environment: claim the task, prepare the prompt, run the
   `develop` container workflow, merge the result, close the task, clean up.

With the starter tasks: the first pass runs *Validate Agent works* (the agent
creates `./agent_test` and the change is merged to your branch); the next
pass finds *Clean up cloche test files* unblocked and runs it, deleting the
validation files. Two commits later your setup is proven end-to-end.

## Watch it

```bash
cloche list            # runs and their states
cloche poll <run-id>   # block until a run finishes (use this, not sleep loops)
cloche status          # current step detail
cloche logs <run-id>   # step logs
```

The web dashboard (default `localhost:8080`) shows the same live.

## Stop it

```bash
cloche loop stop         # stop dispatching; in-flight runs finish and stay resumable
cloche loop stop --hard  # also shut down in-flight runs (use before daemon rebuilds)
cloche loop status       # what would resume if the daemon restarted
```

## When something fails

Most failure paths in the generated `main` workflow route to the **unclaim**
step: it sets the task back to `open` in beads and stops the loop, so a human
can look before anything else runs. Investigate with `cloche logs`, fix the
cause, and run `cloche loop` again. The loop also stops itself when
`stop_on_error` or `max_consecutive_failures` (`.cloche/config.toml`)
triggers.

**Next:** [4. Passing data between steps](4-passing-data-between-steps.md)
