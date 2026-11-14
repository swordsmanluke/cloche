Feature: Saga module reads answers in the shape real Saga emits
  Real "saga show <id> --json" returns each answer's author as a plain string
  ("human" or "agent") and may set an answer's status to null. Reading an
  answered question must work on that shape; it used to fail with "has no
  answer yet".

  # --- L1: read.sh accepts the real shape ---

  Scenario: A real-shaped human answer is read back
    Given a real-shaped saga question with one answer by author "human" with select [1] and status null
    When the saga module reads that question
    Then the read exits 0
    And the read prints the human answer's text

  Scenario: A human answer is preferred over an agent answer
    Given a real-shaped saga question answered by both "agent" and "human"
    When the saga module reads that question
    Then the read prints the human answer's text
    And the read does not print the agent answer's text

  Scenario: Without a human answer the latest non-deprecated answer is read
    Given a real-shaped saga question with an agent answer, a later agent answer and a deprecated agent answer after it
    When the saga module reads that question
    Then the read prints the later non-deprecated agent answer's text

  Scenario: A question with no answers is still reported as unanswered
    Given a real-shaped saga question with no answers
    When the saga module reads that question
    Then the read exits non-zero
    And the read reports the question has no answer yet

  # --- L2: stubs and docs are faithful to real output ---

  Scenario: The saga stubs emit the real answer shape
    When I inspect the stub saga used by the module and feature tests
    Then every stubbed answer has a string author and a null status

  Scenario: No tracked file still documents the object-shaped author
    When I search tracked files for the object-shaped author "kind" fixture
    Then no tracked file contains it

  Scenario: Awaiting answered questions works on real-shaped answers
    Given real-shaped saga questions that have all been answered
    When the saga module awaits those questions
    Then the saga module reports no open questions
