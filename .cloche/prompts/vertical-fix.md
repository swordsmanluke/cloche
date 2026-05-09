# Vertical workflow: fix layer test failures

The implement step finished, but `go test ./...` failed. Read the test output from
the previous step and drive the failures to green.

## Boundaries

You are still inside one layer of a vertical-development run. Do not expand the
scope of the layer to fix tests:

- Do not implement parts of layers below yours. If a test is failing because it
  expects a real DB and you only mocked one, the *test* is wrong for this layer —
  rewrite the test to exercise the mock, or move it to the layer where the real
  implementation will live.
- Do not "fix" tests by deleting them or weakening assertions. If a test genuinely
  doesn't apply at this layer's depth, skip it with a clear `t.Skip("deferred to
  L<n>")` reason rather than removing it.
- Do not refactor surrounding code that isn't directly involved in the failures.

If the failures are deep enough that fixing them in scope is impossible, exit with
the give-up signal — write `$(clo get temp_file_dir)/agent-give-up-reason.md` with
your analysis and exit non-zero. The workflow will surface this as a stuck PR for
the user.

Otherwise: read the failures, fix them, and commit.
