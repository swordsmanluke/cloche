Feature: Extract worktree re-resolves base SHA per sub-workflow

  When a cloche attempt runs multiple sub-workflows in sequence (e.g. a
  vertical workflow across multiple layers), each sub-workflow's git
  extraction must commit on the base SHA current at extract time — not the
  SHA frozen when the extract worktree was first prepared.

  Without this fix, every extraction commits on the original base, producing
  parallel branches instead of a stack. PRs in the stack report add/add
  merge conflicts against each other because none of the branches is
  actually descended from its predecessor.

  Background:
    Given a git repository with an initial commit labelled "base"

  Scenario: Single sub-workflow extract commits on the workflow base
    Given an extract worktree prepared from commit "base"
    When a sub-workflow extracts its results against commit "base"
    Then the extraction commit's parent is commit "base"

  Scenario: Second layer stacks on first when base branch advances to L1
    Given a sub-workflow has extracted against "base" and produced commit "L1"
    And the base branch is advanced to commit "L1"
    When a sub-workflow extracts its results against commit "L1"
    Then the extraction commit's parent is commit "L1"
    And the merge-base of "L1" and the extraction commit is "L1"

  Scenario: Three sequential layers form a linear stack
    Given a sub-workflow has extracted against "base" and produced commit "L1"
    And the base branch is advanced to commit "L1"
    And a sub-workflow has extracted against "L1" and produced commit "L2"
    And the base branch is advanced to commit "L2"
    When a sub-workflow extracts its results against commit "L2"
    Then the extraction commit's parent is commit "L2"
    And the extraction history from "base" to the new commit is linear

  Scenario: Worktree with prior commits is re-anchored before each extraction
    Given an extract worktree prepared from commit "base" that has since advanced with extra commits
    When a new extraction runs against commit "base"
    Then the new extraction commit's parent is commit "base"
    And the new commit is not a descendant of the worktree's prior HEAD

  Scenario: Two extracts against an unchanged base both succeed
    Given the base commit does not change between sub-workflows
    When two sub-workflows extract sequentially against commit "base"
    Then both extraction commits have commit "base" as their parent
    And neither extraction returns an error
