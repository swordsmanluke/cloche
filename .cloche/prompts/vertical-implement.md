# Vertical workflow: implement one layer

Read your context first: `cat $(clo get layer_prompt_path)`. That file describes the
layer task you are implementing, which feature it belongs to, and which branch is
already checked out for you.

## What this step is

You are implementing **one thin top-to-bottom slice** of a larger feature. The
preceding layer (or, for L1, the BDD test plan branch) is already in your base.
The layers below yours don't exist yet — you must **mock** them with committed
code.

Concretely:

- If you are the UI layer, mock the API with hardcoded responses or a fake client.
- If you are the API layer, mock the data layer with in-memory stubs.
- If you are the bottom layer, there is nothing to mock — your job is to replace
  the mocks the previous layer left in place.

The PR you produce should be **demoable end-to-end** at this layer's depth: a
reviewer should be able to run the app, exercise the user-facing path, and see
something that *looks like* the finished feature, even though the work below your
level is faked.

## The BDD test plan is your contract

A test plan exists at `features/` (Gherkin scenarios written and approved before
any layer began). Run it:

```bash
go test ./features/... 2>&1 | tail -30
```

The scenarios that exercise paths your layer is responsible for must transition
from pending/failing → passing. Scenarios that exercise paths handled by deeper
(unimplemented) layers should remain pending, with the step definition's stub
returning a clear "pending: L<n> not yet implemented" error.

**Do not** rewrite or relax existing scenarios to make them pass without real
implementation. **Do** add step bindings as you implement them.

## Hard constraints

1. **Stay in scope.** Do not implement layers below yours. If you find yourself
   reaching for "well, I'll just write the database call too" — stop. That belongs
   in a separate layer. Mock it instead.
2. **Mock, don't skip.** Mocks are committed code with TODOs that mark them for
   replacement, NOT removed functionality. The PR must run.
3. **Keep mocks legible.** Name mocked types/functions clearly (`fakeUserStore`,
   `mockSearchAPI`, etc.) and put them in obvious files so the next-layer agent
   can find and replace them.
4. **Unit tests where they help.** Write unit tests for individual components your
   layer adds. The Gherkin scenarios cover behavior; unit tests cover internal
   contracts. Don't write a unit test that just round-trips mock data — the
   self-review step will flag and remove those.
5. **No drive-by refactors.** Touch only what this layer needs.

## When you discover a new layer

If, while implementing your assigned layer, you realize that the layer below yours
is actually two layers, or you discover a new stratum that wasn't in the original
plan, you should:

1. Create a new bead task with `bd create --parent "$CLOCHE_TASK_ID" --deps
   "<this-layer-id>" --silent --title "[<feature-id>/L<n>] <description>"`.
2. Add a new pending Gherkin scenario or step stub to `features/` that covers what
   the new layer will be responsible for.
3. Mention this in your final commit message so the reviewer can sanity-check.

The workflow's `pick-next-layer` step will pick up the new task automatically
after the current one closes.

## Output

Commit your changes to the branch listed in the context file (it is already checked
out). Use clear, single-purpose commits. The verify, test, and self-review steps
that run after you exit will catch broken builds, failing tests, and common review
errors. You'll get a chance to fix them in the `fix` step.

If you genuinely cannot complete the layer — you hit an unknown, the requirements
in the task description don't specify enough, you need access to something you don't
have — write a short note to `$(clo get temp_file_dir)/agent-give-up-reason.md`
explaining what's blocking you and exit non-zero. The workflow will surface this in
a "stuck" PR for the user to comment on.
