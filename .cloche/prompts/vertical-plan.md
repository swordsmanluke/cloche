# Vertical workflow: plan the feature

You are kicking off a vertical-development run for a feature. The feature task ID is
in `CLOCHE_TASK_ID`. Read it with `bd show "$CLOCHE_TASK_ID" --json` and study the
parent feature description carefully.

## Idempotency check first

Vertical runs can be retried — this step may be re-entered for a feature that already
has its initial layers planned. Before doing anything, run:

```bash
bd list --parent "$CLOCHE_TASK_ID" --json
```

If that returns one or more child tasks whose titles look like layer tasks
(`[<feature-id>/L<n>] ...`) and whose descriptions already specify "what this layer
ships / mocks / acceptance criteria", **do not create new layer tasks**. Verify the
existing layers look correct against the feature description, optionally tighten
their descriptions if something is obviously missing, and exit successfully.

Do not "add a third layer" pre-emptively — later layers are created during
implementation when the agent on layer N discovers it needs an N+1.

Only when no layer children exist (or the existing ones are clearly stubs / wrong /
unrelated) should you proceed with the rest of this prompt.

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
- `bd list --parent "$CLOCHE_TASK_ID"` shows the layer tasks (either the two you
  just created, or the pre-existing ones you verified during the idempotency
  check).
- For any L2 you created, `bd show <L2-id>` shows the L1 dependency.
- `bd ready` lists L1 (no open deps) but NOT L2 (blocked by L1).

If anything looks wrong, fix it before returning. Once the layer tasks are in
place and correctly wired, exit successfully — the workflow takes over from here.
