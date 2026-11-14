Feature: Questions contract and cloche questions CLI
  A project binds a qa backend module. The cloche questions commands let a
  workflow or a person ask questions, poll for answers and read them back,
  without knowing which backend stores them.

  Background:
    Given a project with a stub backend module bound to qa

  # --- L1: CLI over a fake backend ---

  Scenario: Asking questions from a file prints the new ids
    Given a question file with a title, context and two questions
    When I run "cloche questions ask --file questions.yaml"
    Then the command exits 0
    And the output lists one new question id per question

  Scenario: A malformed question file is rejected with a clear error
    Given a question file with a question that has no prompt
    When I run "cloche questions ask --file questions.yaml"
    Then the command exits non-zero
    And the error names the missing prompt
    And no questions are created

  Scenario: --no-judge is accepted and changes nothing yet
    Given a question file with a title, context and two questions
    When I run "cloche questions ask --file questions.yaml --no-judge"
    Then the command exits 0
    And the output lists one new question id per question

  Scenario: Await prints nothing while questions are open
    Given questions have been asked and none answered
    When I run "cloche questions await"
    Then the command exits 0
    And the output is empty

  Scenario: Await prints the answered marker once none are open
    Given questions have been asked and all answered
    When I run "cloche questions await"
    Then the output is "CLOCHE_RESULT:answered"

  Scenario: Read prints the answer, and exits 1 when there is none
    Given a question that has been answered with "use postgres"
    And a question that has not been answered
    When I run "cloche questions read" for the answered question
    Then the command exits 0
    And the output is "use postgres"
    When I run "cloche questions read" for the unanswered question
    Then the command exits 1

  Scenario: Guide prints the bound module's asking and judge guidance
    Given the backend module ships asking.md and judge.md
    When I run "cloche questions guide"
    Then the output is the contents of asking.md
    When I run "cloche questions guide --judge"
    Then the output is the contents of judge.md

  Scenario: Asking with no bound backend names the bind to add
    Given a project with no qa backend bound
    And a question file with a title, context and two questions
    When I run "cloche questions ask --file questions.yaml"
    Then the command exits non-zero
    And the error mentions "bind qa = <module>"

  # --- L2: daemon, HTTP routes, contract resolution ---

  Scenario: Human answer wins when a human and a judge both answered
    Given a question answered by a judge with "judge says A"
    And the same question answered by a human with "human says B"
    When I run "cloche questions read" for that question
    Then the output is "human says B"

  Scenario: HTTP question routes require the project's signal token
    Given the daemon is running with the stub backend bound to qa
    When I POST to the questions ask, await and read routes with no token
    Then each response is rejected as unauthorized
    When I POST to them with an invalid token
    Then each response is rejected as unauthorized
    When I POST to the ask route with the project's signal token
    Then the response succeeds and returns question ids

  Scenario: Any backend module passes the shared conformance suite
    Given the conformance suite and a stub tool backing a backend module
    When the suite runs ask, open, answer, none-open, read and human-beside-judge
    Then every conformance check passes
