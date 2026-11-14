Feature: The CLOCHE_SAGA_ADDR repository scan covers tracked files only
  The "read nowhere in the repository" check must give the same answer in a
  fresh clone and on a host checkout that holds untracked directories such as
  .gitworktrees/ (the daemon's per-run extract worktrees).

  Scenario: Untracked files that mention CLOCHE_SAGA_ADDR are ignored
    Given a checkout with an untracked .gitworktrees/ directory whose files mention "CLOCHE_SAGA_ADDR"
    When the repository is scanned for "CLOCHE_SAGA_ADDR"
    Then the scan reports no matches

  Scenario: A tracked file that mentions CLOCHE_SAGA_ADDR is still reported
    Given a checkout where the tracked file "docs/example.md" mentions "CLOCHE_SAGA_ADDR"
    When the repository is scanned for "CLOCHE_SAGA_ADDR"
    Then the scan reports "docs/example.md"

  Scenario Outline: Existing exclusions still apply to tracked files
    Given a checkout where the tracked file "<path>" mentions "CLOCHE_SAGA_ADDR"
    When the repository is scanned for "CLOCHE_SAGA_ADDR"
    Then the scan reports no matches

    Examples:
      | path                    |
      | features/notes.feature  |
      | .cloche/prompts/x.md    |
      | node_modules/pkg/i.js   |
