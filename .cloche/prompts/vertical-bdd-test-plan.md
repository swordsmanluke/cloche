# Vertical workflow: write the BDD test plan

You are running before any layer implementation. The feature task ID is in
`CLOCHE_TASK_ID`. The test-plan branch (`vertical/<feature>/test-plan`) has already
been checked out for you.

Read the feature description and the planned layer tasks:

```bash
bd show "$CLOCHE_TASK_ID" --json | jq -r '.[0]'
bd list --parent "$CLOCHE_TASK_ID" --all --long
```

## Your job

Write **executable Gherkin scenarios** that describe the feature's expected
user-facing behavior. These are the contract the implementation has to satisfy. They
will all fail (or be marked pending) right now — that's correct, by design. Each
subsequent layer in the vertical stack should make a subset of them pass.

## Framework: godog

Cloche is a Go project. Use **[godog](https://github.com/cucumber/godog)** —
Cucumber's official Go implementation. It reads `.feature` files (Gherkin syntax)
and binds steps to Go functions.

Standard layout:

```
features/
  <feature-slug>.feature          # Gherkin scenarios
  <feature-slug>_test.go          # Step definitions + TestMain that wires godog
```

The `_test.go` file should:

1. Be in package `<package>_test` (or the package being tested if integration is
   tighter).
2. Define `TestMain` that calls godog's runner against `features/`.
3. Bind every step in your `.feature` file to a Go function. Step bodies should
   `return errors.New("pending: <layer> implementation")` so the suite fails loudly
   today and lights up green as layers land.

If `features/` already exists with godog wired up for a previous feature, add a new
`.feature` file alongside the existing ones; do not duplicate the runner setup.

If godog is not yet a dependency of this project, add it (`go get
github.com/cucumber/godog@latest`) and check the import in the new test file
compiles.

## What to write

Scenarios should exercise **user-facing controls in simulation/test**, not internal
APIs. Think:

- "Given a user is on the saved-searches page, when they click 'Save', then..."
- "Given the daemon is running, when the user runs `cloche list`, then the output
  contains..."

These are end-to-end behavioral specs, not unit tests. Unit tests for individual
internal components are written separately during layer implementation.

Cover the **happy path first**, then 1-2 important edge cases per major user-facing
flow. Don't over-spec — these scenarios will outlive the implementation, and every
brittle one is a future maintenance burden.

## Verification

Before reporting success:

```bash
go test ./features/... 2>&1 | head -40
```

The tests should **discover and fail** (with the "pending" errors). If `go test`
fails to compile or to find the suite, fix that. Failing scenarios are correct;
compile errors are not.

Commit your changes to the current branch (already checked out) with a clear
message like `Add BDD test plan for <feature title>`.

## PR description

Before exiting, write a focused PR description to
`$(clo get temp_file_dir)/pr-description.md`. The host's open-test-plan-pr step
picks this up verbatim as the PR body, so make it specific to this test plan:

1. **Scope** — 2-4 bullets naming the .feature files you added, with a one-line
   summary of what each covers (e.g., "DSL parsing of `repository` blocks: single
   repo, multiple repos, step-level `repository = "name"` field").
2. **Layer mapping** — which scenarios light up at which layer (L1/L2/...), so the
   reviewer can sanity-check that the test plan matches the planned layer split.
3. **Open questions** — any design ambiguity in the feature description that you
   resolved one way and that a reviewer might reasonably want to flip. If you
   didn't have to make any judgment calls, say "None — feature description was
   unambiguous." (Don't pretend.)
4. **How a reviewer can verify** — `go test ./features/...` and what they should
   see (e.g., "12 scenarios, all pending").

Keep it tight, under ~30 lines. This is the reviewer's read on whether the
contract you've written matches the feature they wanted — wishy-washy template
language ("scenarios that capture the feature's intent") wastes their time.

## Hard constraints

1. **No implementation code.** Only `.feature` files, step definitions with pending
   stubs, and the godog wiring. If you find yourself writing real logic, stop.
2. **No mocks of the project's domain code.** That belongs in layer implementation.
3. **No drive-by edits to existing project files** beyond adding godog to `go.mod`
   if needed.
