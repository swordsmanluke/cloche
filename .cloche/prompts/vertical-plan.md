# Vertical workflow: plan the feature

You are kicking off a vertical-development run for a feature. The feature task ID is
in `CLOCHE_TASK_ID`. Read it with `bd show "$CLOCHE_TASK_ID" --json` and study the
parent feature description carefully.

## Your job

Produce the **first two layer tasks** for this feature, in bead, as children of the
feature task with the right dependencies wired up.

A layer is one thin top-to-bottom slice of the feature. The first layer is almost
always the user-facing surface (UI, CLI command, API endpoint visible to a caller),
because the value of vertical development comes from the user being able to see the
feature take shape and steer it before deeper work commits us to the wrong path.

Subsequent layers progressively replace the mocks introduced in upper layers with real
implementation:

- Layer 1 (UI) — mocks the API
- Layer 2 (API) — real API, mocks the data layer
- Layer 3 (DB) — real persistence, removes the API mocks

You only need to plan **the first two**. Later layers will be created during
implementation, when the agent working on layer N realizes it needs a layer N+1.

## How to create the layer tasks

Use `bd create` with `--parent` and `--deps`:

```bash
# Layer 1 — no deps, child of the feature task
L1_ID=$(bd create --parent "$CLOCHE_TASK_ID" --silent \
  --title "[$CLOCHE_TASK_ID/L1] <UI/surface description>" \
  --description "<what this layer ships, what it mocks, acceptance criteria>")

# Layer 2 — depends on Layer 1 (so the picker only schedules it after L1 closes)
bd create --parent "$CLOCHE_TASK_ID" --silent --deps "$L1_ID" \
  --title "[$CLOCHE_TASK_ID/L2] <API/middle description>" \
  --description "<what this layer ships, what it mocks, acceptance criteria>"
```

For each task, the **description** must specify:

- What this layer ships (which files/components are expected to change).
- What this layer mocks (concrete names — `fakeUserStore`, `mockSearchAPI`, etc.).
- Acceptance criteria the user should verify when reviewing the PR.

## Sanity check before you finish

- The feature task itself remains `in_progress`; do NOT close it.
- `bd list --parent "$CLOCHE_TASK_ID"` shows both new tasks.
- `bd show <L2-id>` shows the L1 dependency.
- `bd ready` lists L1 (no open deps) but NOT L2 (blocked by L1).

If anything looks wrong, fix it before returning. Once the two layer tasks are in
place and correctly wired, exit successfully — the workflow takes over from here.
