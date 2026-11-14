Feature: Built-in generic judge started on cloche questions ask
  `cloche questions ask` runs the bound ask slot, then starts exactly one judge
  run for the new question ids. The judge is a built-in host workflow named
  "judge", replaceable by a project workflow of the same name. Its prompt is
  the generic rules followed by the bound module's judge.md.

  Background:
    Given a project whose qa slot is bound to a module that ships a judge.md
    And a fake agent runner and an open budget gate

  # --- L1: launch surface (mock launcher) ---

  Scenario: Asking starts exactly one judge run for the created ids
    When I ask 3 questions with "cloche questions ask"
    Then the 3 new question ids are printed
    And exactly one judge launch is requested, carrying those 3 ids

  Scenario: --no-judge skips the judge
    When I ask 2 questions with "cloche questions ask --no-judge"
    Then the 2 new question ids are printed
    And no judge launch is requested

  Scenario: [questions] judge = false skips the judge
    Given .cloche/config.toml sets "[questions]" "judge = false"
    When I ask 2 questions with "cloche questions ask"
    Then no judge launch is requested

  Scenario: A judge that cannot launch does not fail the ask
    Given the judge launcher fails
    When I ask 2 questions with "cloche questions ask"
    Then the judge ask command exits 0
    And the 2 new question ids are printed

  Scenario: Validate warns when a qa-bound module has no judge.md
    Given the bound qa module has no judge.md
    When I run "cloche validate"
    Then the validate output warns that the qa module has no judge.md
    And the validate command exits 0

  Scenario: Validate does not warn when the qa module has a judge.md
    When I run "cloche validate"
    Then the validate output has no judge.md warning

  Scenario: A qa module without judge.md gets no judge run
    Given the bound qa module has no judge.md
    When I ask 1 questions with "cloche questions ask"
    Then no judge run is started

  # --- L2: real judge ---

  Scenario: The judge prompt is the generic rules followed by judge.md
    When I ask 1 questions with "cloche questions ask"
    Then the judge run's prompt contains the rules on citing sources, never replacing a human answer, leaving unsure questions unanswered, stating false premises as the answer, and leaving permission requests unanswered
    And the module's judge.md text appears after those rules

  Scenario: The judge run is recorded with token usage
    When I ask 1 questions with "cloche questions ask"
    Then run history lists a judge run
    And that run reports token usage

  Scenario: A closed budget gate prevents the judge from starting
    Given the budget launch gate is closed
    When I ask 1 questions with "cloche questions ask"
    Then no judge run is started
    And the judge ask command exits 0
    And the questions remain unanswered

  Scenario: A project workflow named judge replaces the built-in
    Given the project defines a host workflow named "judge"
    When I ask 1 questions with "cloche questions ask"
    Then the project's judge workflow runs instead of the built-in one

  Scenario: A failed judge leaves the questions unanswered
    Given the judge run fails
    When I ask 2 questions with "cloche questions ask"
    Then the judge ask command exits 0
    And the questions remain unanswered
