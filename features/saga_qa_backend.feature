Feature: Saga module as a qa backend
  A project binds the saga module to the qa contract. The cloche questions
  commands then ask, poll and read answers through Saga, with no judge
  workflow or blocking wait in the module.

  Background:
    Given a project with "bind qa = saga" and SAGA_PROJECT set in the bind settings
    And a stub saga binary on PATH

  # --- L1: new module surface ---

  Scenario: Asking through the saga backend returns question ids
    Given a question file whose backend.feature is "cloche/qa-backends"
    When I run "cloche questions ask --file questions.yaml" against the saga backend
    Then the saga scenario command exits 0
    And one question id per question is printed
    And the stub saga recorded the questions under the bind's SAGA_PROJECT and that feature

  Scenario: Awaiting an unanswered question reports it as still open without blocking
    Given questions asked through the saga backend that nobody has answered
    When I run "cloche questions await" over those ids against the saga backend
    Then the command returns promptly
    And the saga backend reports those questions as open

  Scenario: Awaiting answered questions reports none open
    Given questions asked through the saga backend that have all been answered
    When I run "cloche questions await" over those ids against the saga backend
    Then the saga backend reports no open questions

  Scenario: Reading an answer returns the ruling in force
    Given a saga question with a judge ruling
    When I run "cloche questions read" for that id against the saga backend
    Then the printed answer path holds the judge ruling

  Scenario: A human's answer is preferred over the judge's
    Given a saga question with both a judge ruling and a human answer
    When I run "cloche questions read" for that id against the saga backend
    Then the printed answer path holds the human answer

  Scenario: The saga module passes the shared qa conformance test
    When the shared qa conformance test runs the saga module with the stub saga
    Then every conformance check passes

  Scenario: The module ships Saga-specific judge and asking guidance
    When I list the saga module's files
    Then judge.md and asking.md exist
    And judge.md mentions "saga guide", "saga search --follow", "saga answer --judge" and "saga hold"

  # --- L2: removal of saga-judge ---

  Scenario: The old saga-judge module is gone
    When I list the modules under .cloche/modules
    Then there is no saga-judge module
    And no file in the repo mentions saga_batch_id, saga_batch_file, saga_outcome or reframe

  Scenario: The repository still validates after the rewrite
    When I run "cloche validate" on this repository
    Then the saga scenario command exits 0

  Scenario: The modules docs describe the saga qa backend
    When I read the "Asking in Saga" section of docs/modules.md
    Then it describes binding saga to qa with SAGA_PROJECT
    And it does not describe a judge workflow or blocking await
