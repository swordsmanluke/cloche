# Cloche: Vertical Development Workflow

## Motivation

Most development workflows in Cloche today implement a *whole feature* in a single
container run: agent reads task → writes code → tests → opens PR. This works for small,
well-scoped tasks but fails for non-trivial features where the user needs to validate
direction *before* the agent has spent hours building the wrong thing underneath.

The **vertical** workflow flips this around. Instead of one large pass, a feature is
built in three phases:

1. **Test plan.** An agent reads the feature description and writes Gherkin/godog
   scenarios that capture the user-facing behavior. PR for user approval.
2. **Layer loop.** The feature is built in thin top-to-bottom slices. The first layer
   is the user-facing surface, with everything below it mocked. Each subsequent layer
   replaces one set of mocks with real implementation. PR per layer; user approval
   gate between every layer.
3. **Documentation.** After the final layer is approved, an agent updates project
   docs to reflect the new feature. PR for user approval.

Then `finalize` squash-merges the entire stack into the user-specified base branch
as a single commit.

The polling gate at every phase is the entire point: each PR is a checkpoint where
the user can confirm the feature is heading the right direction *before* committing
more work. Days of waiting between phases is fine.

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
plan-feature ─▶ bdd-test-plan ─▶ open-test-plan-pr ─▶ poll-test-plan-pr ─┐
                                                                          │
   ┌──────────────────────────────────────────────────────────────────────┘
   │           feedback loop ──┐
   ▼              ▲            │
record-test-plan  │       address-test-plan-feedback
   │              │            ▲
   ▼              │            │
[layer loop ◄─────┴── pick-next-layer ─┐
   │ implement-layer → open-pr →       │
   │ poll-pr → close-layer → ─────┐    │
   │ (or feedback → address)     │    │
   └─────────────────────────────┘    │
                                       │
   ┌───────────────────────────────────┘
   ▼
update-docs ─▶ open-docs-pr ─▶ poll-docs-pr ─▶ finalize ─▶ done
                                    │
                                    └─▶ feedback → address-docs-feedback
```

### Steps

| Step | Type | Purpose |
|------|------|---------|
| `plan-feature` | agent (host) | Reads the feature task; creates the first two child layer tasks via `bd create --parent --deps`. |
| `bdd-test-plan` | sub-workflow | Container; agent writes `.feature` files (godog) + step stubs that fail loudly; commits to `<feature>-test-plan` branch. |
| `open-test-plan-pr` | script | Pushes test-plan branch; opens PR targeting `vertical_base_branch`. |
| `poll-test-plan-pr` | poll | 60s; emits `approved`, `feedback`, or pending. |
| `address-test-plan-feedback` | sub-workflow | Reuses `address-pr-feedback` to address review comments and re-push. |
| `record-test-plan` | script | Sets `test_plan_branch` KV so `pick-next-layer` knows L1's base. |
| `pick-next-layer` | script | `bd list --parent <feature>`; picks topmost open child with all deps closed; sets `current_layer_id` and `current_base_branch` (test-plan branch for L1, previous closed layer's branch for later layers). |
| `implement-layer` | sub-workflow | Container; reads layer task, implements with mocks, runs tests + self-review, commits to `<feature>-<layer-id>` branch. |
| `open-pr` | script | Pushes layer branch; opens PR targeting the layer's base. Picks "ready for review" or "stuck, here's what I tried" body template based on `implement_status` KV. |
| `poll-pr` | poll | Same poll script; reads `current_pr_number`. |
| `address-feedback` | sub-workflow | `address-pr-feedback` again. |
| `close-layer` | script | Marks the layer task `closed` in bead. **Does NOT merge** — Strategy B leaves PR approved-but-open. Clears layer-scoped KV. |
| `update-docs` | sub-workflow | Container; agent updates project docs (NOT inline code comments) on `<feature>-docs` branch. |
| `open-docs-pr` | script | Opens PR targeting the bottom-most layer's branch. |
| `poll-docs-pr` | poll | Same poll script. |
| `address-docs-feedback` | sub-workflow | `address-pr-feedback` once more. |
| `finalize` | script | `git merge --squash <feature>-docs` into `vertical_base_branch`; commits one squashed commit; pushes; closes feature task. |

### `implement-vertical-layer` (container sub-workflow)

```
read-layer → implement → verify → test → self-review → mark-success → done
                                  ↑                            
                                  └─ fix ─┘  (test:fail → fix → test)
```

Any of `implement:fail`, `verify:fail`, `fix:fail`, `fix:give-up`,
`self-review:fail`, `self-review:give-up` route to `mark-stuck → done`. The
sub-workflow always terminates successfully (`done`); the host workflow checks
`implement_status` (KV: `success` or `stuck`) to choose the PR body template.

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

### `address-pr-feedback` (container sub-workflow)

Generic across all three PR phases (test plan, layer, docs). Pulls open comments
into a markdown context file, runs an agent against them, runs verify, pushes the
branch. Updates `last_addressed_at` KV so the next poll doesn't immediately
re-fire on the comments we just handled.

## Polling semantics

The same `vertical-poll-pr.sh` script drives all three poll steps. It reads
`current_pr_number` from KV — every `open-*-pr` script writes that, so each poll
operates on the most recent PR.

| GitHub state | Result |
|--------------|--------|
| At least one APPROVED review, no outstanding CHANGES_REQUESTED | `approved` |
| Any CHANGES_REQUESTED review, OR any unresolved comment newer than `last_addressed_at` | `feedback` |
| Otherwise (pending review, draft, etc.) | pending (exit 0 no marker) |
| PR closed without merge | `fail` |

60s interval. No step timeout — multi-day waits are normal.

## Stack and merge model (Strategy B)

```
vertical_base_branch (e.g. main)
 └─ vertical/<feature>/test-plan         PR #1 — Gherkin specs
     └─ vertical/<feature>/<L1-id>       PR #2 — UI layer (some scenarios pass)
         └─ vertical/<feature>/<L2-id>   PR #3 — API layer (more scenarios pass)
             └─ vertical/<feature>/<L3-id>   PR #4 — DB layer (all pass)
                 └─ vertical/<feature>/docs  PR #5 — documentation
```

Each PR targets the previous branch in the stack (not `vertical_base_branch`).
PRs accumulate approved-but-unmerged through the run; `close-layer` and the
test-plan/docs polls only update bead state, not git refs.

`finalize` performs the merge:

```bash
git checkout <vertical_base_branch>
git pull
git merge --squash origin/vertical/<feature>/docs
git commit -m "<feature title>" -m "<full feature summary>"
git push
```

The squash-merge captures every commit between base and the docs branch — i.e.,
test plan + every layer + docs — as one commit on the base branch. Per-PR review
history is preserved on the GitHub side; the final base history is one clean
commit per feature.

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
4. **Merge policy.** Strategy B: defer all merges to `finalize`, which performs a
   single `git merge --squash` of the docs branch into the base. One commit on the
   base per feature.
5. **Failure / parking lot.** `implement-layer` always terminates `done`;
   `implement_status` KV signals success or stuck. Stuck → "needs help" PR body.
   Same poll/feedback loop handles user guidance.
6. **BDD framework.** godog (Cucumber for Go). `.feature` files in `features/`,
   step definitions in `features/*_test.go`.
7. **Self-review checks.** Five categories: dumb tests, dead code, DRY, swallowed
   errors, log-and-rethrow. No new functionality, no out-of-diff edits.
8. **Docs scope.** Project docs (`docs/`, `README.md`, `CHANGELOG.md`, etc.) only.
   Inline code comments stay in the layer that touched them.
