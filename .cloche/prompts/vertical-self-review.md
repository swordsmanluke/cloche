# Vertical workflow: self-review

The implement and test steps are done — tests pass on the layer branch. Before this
PR goes up to the user, do one focused self-review pass to catch and fix common
errors that humans waste cycles flagging.

## What to look for

Read the layer's diff against its base branch:

```bash
git diff "$(clo get current_base_branch)..HEAD"
```

Then check the diff for each of the issues below. **Only modify code that has a
real problem** — if a section is fine, leave it alone. Drive-by stylistic edits
are not in scope.

### 1. Dumb tests (high priority)

Unit tests that "round-trip mock data" without exercising any real behavior or
logic. Symptoms:

- Test sets up a mock, calls the mocked function, asserts the mock returned what
  was set up. (Tests the mock, not the code.)
- Test injects a fake result into a struct, calls a getter, asserts the field
  matches. (Tests Go's struct semantics, not your code.)
- Test calls a function that delegates entirely to a mocked dependency, with no
  branching, transformation, or invariant of its own.

**Fix:** delete the test. If there is genuinely behavior to verify but you'd need
to test the integration of two real components, either rewrite as a real
integration test (using a test double, not a mock that just returns canned values)
or move the assertion into a Gherkin scenario in `features/`.

### 2. Dead code

Functions, types, fields, or branches added in this diff that are never called or
read. Common causes: refactor partway, agent generated speculative scaffolding,
deleted call site but kept the helper.

**Fix:** delete it. If you're certain it's needed but currently unused (e.g.,
exported for use by a future layer), leave a one-line comment explaining why.
Otherwise: delete.

### 3. DRY violations

Code duplicated within the diff (or duplicated against existing code that the diff
touches). Two near-identical blocks where one helper would do.

**Fix:** extract the helper. Do not extract speculatively — only when the
duplication is real and the resulting abstraction is at least as readable as the
duplicated form.

### 4. Swallowed errors

Errors caught and discarded:

- `_, _ = someCall()`
- `if err != nil { /* nothing */ }`
- `defer f.Close() // ignore` when Close errors matter (writes, sync flushes)

**Fix:** handle the error. Either return it, log it once at the top of the call
chain, or — if discarding is genuinely correct — add a comment explaining why
ignoring it is safe.

### 5. Log-and-rethrow

The code logs an error AND returns/re-raises it, OR it logs an error at multiple
levels of the call chain. Both produce duplicate log lines in production.

**Rule:** log errors **once, at the top** of the call chain. Inner layers should
return errors with context (`fmt.Errorf("doing X: %w", err)`), not log them.
Logging happens at the boundary where the error is actually handled (the request
handler, the daemon's main loop, etc.).

**Fix:** remove inner-layer logs. Confirm the outer layer logs the error
exactly once before returning to its caller (or a 500, or whatever the boundary
is).

## Output

Commit your fixes with a clear message (`Self-review fixes: remove dumb tests,
clean up swallowed errors`). After your fixes are in:

```bash
go test ./... 2>&1 | tail -20
```

If tests now fail because of your changes, fix them in the same commit. Only
report success when tests pass.

## Hard limits

1. **No new functionality.** Only deletions, simplifications, and direct fixes
   to the issues above.
2. **No drive-by refactors** outside the diff.
3. **Don't touch the BDD scenarios in `features/`** — those were approved by the
   user.
4. If the diff is genuinely clean — no issues found — that is fine. Report
   success without making changes.
5. If you find an issue that's deeper than this step is meant to fix (e.g., the
   whole layer's architecture is wrong), write
   `$(clo get temp_file_dir)/agent-give-up-reason.md` and exit non-zero with
   give-up. Don't paper over structural problems with self-review fixes.
