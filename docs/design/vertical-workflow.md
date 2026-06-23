# Cloche: Vertical Development Workflow

> **See also:** [Run Isolation & Safety](../run-isolation/index.md) documents the
> per-run isolation that makes this workflow safe to run concurrently — the clean
> per-run container snapshot, the throwaway-worktree `finalize` with
> sync-forward-before-merge-back, `close-on-publish`, `--allow-no-op` idempotent
> re-runs, and resume-rebuild. This document describes the *workflow shape*; that
> one describes how a run is kept from corrupting the base branch.

## Motivation

Most development workflows in Cloche today implement a *whole feature* in a single
container run: agent reads task → writes code → tests → opens PR. This works for small,
well-scoped tasks but fails for non-trivial features where the user needs to validate
direction *before* the agent has spent hours building the wrong thing underneath.

The **vertical** workflow flips this around. Instead of one large pass, a feature is
built in three phases:

1. **Test plan.** An agent reads the feature description and writes Gherkin/godog
   scenarios that capture the user-facing behavior. Branch pushed to origin automatically.
2. **Layer loop.** The feature is built in thin top-to-bottom slices. The first layer
   is the user-facing surface, with everything below it mocked. Each subsequent layer
   replaces one set of mocks with real implementation. Branches pushed automatically;
   a stuck layer fails the job immediately so a human can investigate.
3. **Documentation.** After the final layer completes, an agent updates project
   docs to reflect the new feature. Branch pushed to origin automatically.

Then `finalize` fast-forward-merges the entire stack into the user-specified base
branch and deletes the stack branches from origin.

## Concepts

| Term | Meaning |
|------|---------|
| **Feature task** | The parent bead task. Identified via `CLOCHE_TASK_ID`. |
| **Layer task** | A bead **child** of the feature task (`bd create --parent <feature>`). Represents one slice. |
| **Layer** | One slice: a self-contained PR that adds the code for one stratum and mocks everything below. |
| **Mock** | Code committed in an upper layer that simulates a not-yet-built lower layer. Replaced by the lower layer's PR; gone or migrated to test doubles by the bottom layer. |
| **Stack** | The chain of branches: `<base>` → `<feature>-test-plan` → `<feature>-L1` → `<feature>-L2` → ... → `<feature>-docs`. Each PR targets the previous branch in the chain. |

Layers are flexible per feature. UI is the default first layer when the feature has
any user-facing surface. Pure backend tasks drop the UI layer and start one rung lower.

## Bead task structure

When a feature task is picked up, `plan-feature` seeds the next two child layer tasks
via `bd create --parent <feature-id> --deps <prev-layer-id>`. Subsequent layers are
added during implementation as the agent discovers them.

```
feature: "Add saved searches"          (parent, in-progress)
├── L1: "[FT-100/L1] Saved searches UI"  (closed, PR #421 approved)
├── L2: "[FT-100/L2] Saved searches API" (in-progress, PR #428 awaiting review)
└── L3: "[FT-100/L3] Saved searches DB"  (open, created during L2 work, blocked by L2)
```

The picker uses `bd list --parent <feature-id>` to enumerate children, picks the
oldest open one whose dependencies are all closed. Layers are processed strictly
serially.

## Workflow shape

The vertical workflow is a **host workflow** — it spans days and multiple PRs. The
phases that produce code (test plan, each layer, docs) run as **container
sub-workflows** so the agent gets a clean environment.

### High-level phases

```
plan-feature ─▶ bdd-test-plan ─▶ publish-test-plan ─▶ record-test-plan
                                                              │
   ┌──────────────────────────────────────────────────────────┘
   ▼
[layer loop ◄──────────────── pick-next-layer ──────────────────────────┐
   │ implement-layer → publish-layer → check-layer-status               │
   │       ok: close-layer ──────────────────────────────────────────┐  │
   │       stuck: job failure (help-needed report in logs)           │  │
   └──────────────────────────────────────────────────────────────┘  │
                                                                       │
   ┌───────────────────────────────────────────────────────────────────┘
   ▼
update-docs ─▶ broader-docs ─▶ publish-docs ─▶ finalize ─▶ done
```

### Steps

| Step | Type | Purpose |
|------|------|---------|
| `plan-feature` | agent (host) | Reads the feature task; creates the first two child layer tasks via `bd create --parent --deps`. |
| `bdd-test-plan` | sub-workflow | Container; agent writes `.feature` files (godog) + step stubs that fail loudly; commits to `<feature>/test-plan` branch. |
| `publish-test-plan` | script | Pushes the test-plan branch to origin. No PR, no human gate. |
| `record-test-plan` | script | Sets `test_plan_branch` KV so `pick-next-layer` knows L1's base. |
| `pick-next-layer` | script | `bd list --parent <feature>`; picks topmost open child with all deps closed; sets `current_layer_id` and `current_base_branch`. |
| `implement-layer` | sub-workflow | Container; reads layer task, implements with mocks, runs tests + self-review, commits to `<feature>/<layer-id>` branch. |
| `publish-layer` | script | Pushes the layer branch to origin. No PR, no human gate on the happy path. |
| `check-layer-status` | script | Reads `implement_status` KV. `ok` → close-layer; `stuck` → surfaces the help-needed report from `temp_file_dir/stuck-report.md` and routes to job failure. |
| `close-layer` | script | Marks the layer task `closed` in bead. Clears layer-scoped KV. |
| `update-docs` | sub-workflow | Container; agent updates project docs (NOT inline code comments) on `<feature>/docs` branch. |
| `broader-docs` | sub-workflow | Container; agent updates broader project-level docs. Failure here does not block the docs branch from being published. |
| `publish-docs` | script | Pushes the docs branch to origin. No PR, no human gate. |
| `finalize` | script | Fetches the full branch stack, rebases the top onto the current base, fast-forward-merges base to the rebased top, pushes base, deletes all stack branches from origin, closes the feature task. |

### `implement-vertical-layer` (container sub-workflow)

```
read-layer → implement → verify → test → self-review → mark-success → done
                                  ↑                            
                                  └─ fix ─┘  (test:fail → fix → test)

Any failure path: → document-stuck → mark-stuck → done
```

Any of `implement:fail`, `verify:fail`, `fix:fail`, `fix:give-up`,
`self-review:fail`, `self-review:give-up` route to `document-stuck` first, which
writes a consolidated help-needed report to `temp_file_dir/stuck-report.md`, then
`mark-stuck → done`. The sub-workflow always terminates in `done`; the host
workflow's `check-layer-status` step reads `implement_status` (KV: `success` or
`stuck`) — on `stuck` it surfaces the report in `cloche logs` and fails the job.

`self-review` is a focused pass that catches and fixes:
- Tests that round-trip mock data without exercising real behavior or logic.
- Dead code introduced by the diff.
- DRY violations within the diff.
- Swallowed errors (`_, _ = ...`, empty `if err != nil` blocks).
- Log-and-rethrow (errors logged at multiple levels of the call chain rather than
  exactly once at the top).

The self-review prompt is bounded — only the diff, only these issues, no drive-by
refactors. The agent must re-run tests after any fix and only report success when
green.

## Stack and merge model

```
vertical_base_branch (e.g. main)
 └─ vertical/<feature>/test-plan     — Gherkin specs
     └─ vertical/<feature>/<L1-id>   — UI layer (some scenarios pass)
         └─ vertical/<feature>/<L2-id>   — API layer (more scenarios pass)
             └─ vertical/<feature>/<L3-id>   — DB layer (all pass)
                 └─ vertical/<feature>/docs  — documentation
```

Each branch is a linear descendant of the one below it. No PRs are opened during
the run; branches are pushed straight to origin after each phase so the next
phase's container can base off them.

`finalize` performs the merge:

```bash
# rebase top-of-stack onto current base (handles base moving during the run)
git checkout vertical/<feature>/docs
git rebase origin/<base>
# fast-forward base to the rebased top
git checkout <base>
git merge --ff-only vertical/<feature>/docs
git push origin <base>
# delete all stack branches from origin
git push origin --delete vertical/<feature>/test-plan
git push origin --delete vertical/<feature>/<L1-id>
# ... (all layers) ...
git push origin --delete vertical/<feature>/docs
```

The fast-forward captures every commit in the stack as individual commits on the
base branch. If the rebase produces genuine conflicts (base moved incompatibly),
`finalize` fails loudly with instructions for manual resolution.

## Configuration

The operator can pre-set task-scoped KV before running the workflow:

```bash
CLOCHE_TASK_ID=<feature-task-id> cloche set vertical_base_branch <branch>
CLOCHE_TASK_ID=<feature-task-id> cloche run vertical
```

`vertical_base_branch` defaults to `main`. There are no env-var-only configuration
points — task-scoped KV is preferred everywhere because env vars don't propagate
cleanly between host and container, and they are global to the host process.

## Resolved decisions

1. **Repository scoping.** Deferred. The Repository primitive will be built *using*
   this vertical workflow.
2. **Layer ordering.** Bead `--parent` + `--deps`. Picker uses `bd list --parent`
   then dep-closed checks. No new bead schema field.
3. **Branch strategy.** Stacked PRs. Each layer's PR base = previous branch in the
   stack.
4. **Merge policy.** `finalize` rebases the top of the stack onto the current base,
   then fast-forward-merges the base to the rebased top. Each commit in the stack
   lands individually on the base branch. Stack branches are deleted after merge.
5. **Failure / parking lot.** `implement-layer` always terminates `done`;
   `implement_status` KV signals success or stuck. Stuck → `document-stuck` writes
   a help-needed report; `check-layer-status` surfaces it in logs and fails the job.
6. **BDD framework.** godog (Cucumber for Go). `.feature` files in `features/`,
   step definitions in `features/*_test.go`.
7. **Self-review checks.** Five categories: dumb tests, dead code, DRY, swallowed
   errors, log-and-rethrow. No new functionality, no out-of-diff edits.
8. **Docs scope.** Project docs (`docs/`, `README.md`, `CHANGELOG.md`, etc.) only.
   Inline code comments stay in the layer that touched them.
