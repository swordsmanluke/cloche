Feature: Built-in generic judge started on cloche questions ask
  Asking questions through the bound qa backend also starts one judge run that
  may answer them from cited supporting information. A project can replace the
  judge workflow, turn it off, or leave it to the budget gate.

  Background:
    Given a judge project with a module bound to qa that ships a judge.md

  # --- L1: launch surface (mock launcher) ---

  Scenario: Asking starts exactly one judge run for the new ids
    When the judge user asks two questions
    Then the ask exits 0 and prints two question ids
    And exactly one judge run was started carrying those two ids

  Scenario: --no-judge starts no judge
    When the judge user asks two questions with --no-judge
    Then the ask exits 0 and prints two question ids
    And no judge run was started

  Scenario: Config switch turns the judge off
    Given the judge config sets "[questions] judge = false"
    When the judge user asks two questions
    Then no judge run was started

  Scenario: Validate warns when a qa module has no judge.md
    Given the bound module has no judge.md
    When the judge user runs "cloche validate"
    Then the validate output warns that the qa module has no judge.md
    And the judge user asks two questions
    And no judge run was started

  Scenario: Validate is quiet when judge.md exists
    When the judge user runs "cloche validate"
    Then the validate output has no judge.md warning

  # --- L2: real judge ---

  Scenario: The judge prompt is the generic rules followed by the module's judge.md
    When the judge user asks two questions
    Then the judge prompt states the rules to cite sources, never replace a human answer, leave unsure questions unanswered, state a false premise as the answer, and leave permission requests unanswered
    And the judge prompt ends with the contents of the module's judge.md

  Scenario: The judge is a recorded run with token usage
    When the judge user asks two questions
    Then the judge run appears in run history with token usage

  Scenario: A closed budget gate means no judge run
    Given the judge budget launch gate is closed
    When the judge user asks two questions
    Then the ask exits 0 and prints two question ids
    And no judge run was started

  Scenario: A project workflow named judge replaces the built-in
    Given the project defines its own workflow named "judge"
    When the judge user asks two questions
    Then the judge run uses the project workflow, not the built-in one

  Scenario: A failed judge leaves the questions unanswered and the ask succeeds
    Given the judge run will fail
    When the judge user asks two questions
    Then the ask exits 0 and prints two question ids
    And both questions remain unanswered
