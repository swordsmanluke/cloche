Feature: Stale-reference scan ignores untracked files
  The saga_qa_backend "no file in the repo mentions ..." step must inspect only
  files tracked by git, so a host checkout with leftover .gitworktrees/ from
  earlier daemon runs does not fail `go test ./features/`.

  Scenario: A clean checkout passes the stale-reference scan
    Given a git checkout whose tracked files mention no stale saga keys
    When the stale-reference scan runs
    Then the scan passes

  Scenario: An untracked .gitworktrees copy of the old saga-judge module is ignored
    Given a git checkout whose tracked files mention no stale saga keys
    And an untracked ".gitworktrees/run-1/saga-judge" directory that mentions a stale saga key
    When the stale-reference scan runs
    Then the scan passes

  Scenario: A tracked file with a stale key still fails the scan
    Given a git checkout whose tracked files mention no stale saga keys
    And a tracked file "internal/x.go" that mentions a stale saga key
    When the stale-reference scan runs
    Then the scan fails naming "internal/x.go"

  Scenario: Excluded tracked files may still mention the keys
    Given a git checkout whose tracked files mention no stale saga keys
    And a tracked file "docs/plans/old.md" that mentions a stale saga key
    And a tracked file "features/saga_qa_backend.feature" that mentions a stale saga key
    When the stale-reference scan runs
    Then the scan passes
