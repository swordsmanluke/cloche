Feature: qa phase failures show the underlying error
  When a qa module's phase script (ask, await-answer, read-answer) fails,
  `cloche questions` must say which step failed and show what it printed, and
  the calling run's step log must keep that output, instead of only reporting
  "phase workflow failed".

  Background:
    Given a project whose bound qa module has phase scripts that fail with "boom: no such feature"

  # --- L1: CLI error formatting (phase result faked) ---

  Scenario: Ask failure shows the failing step and its output
    When the qa phase failure output user runs "cloche questions ask --file questions.yaml"
    Then the qa phase failure output command exits non-zero
    And the qa phase failure output error contains "ask workflow finished with state failed: phase workflow failed"
    And the qa phase failure output error names the failing step "ask"
    And the qa phase failure output error contains "boom: no such feature"

  Scenario: Await failure shows the failing step and its output
    When the qa phase failure output user runs "cloche questions await --id q1"
    Then the qa phase failure output command exits non-zero
    And the qa phase failure output error names the failing step "await-answer"
    And the qa phase failure output error contains "boom: no such feature"

  Scenario: Read failure shows the failing step and its output
    When the qa phase failure output user runs "cloche questions read --id q1"
    Then the qa phase failure output command exits non-zero
    And the qa phase failure output error names the failing step "read-answer"
    And the qa phase failure output error contains "boom: no such feature"

  Scenario: Only the tail of very long output is shown
    Given the failing script prints 500 lines before "boom: no such feature"
    When the qa phase failure output user runs "cloche questions ask --file questions.yaml"
    Then the qa phase failure output error contains "boom: no such feature"
    And the qa phase failure output error is shorter than the full script output

  # --- L2: real daemon capture ---

  Scenario: Ask failure output reaches the CLI from the real daemon
    When the qa phase failure output user runs "cloche questions ask --file questions.yaml" against the real daemon
    Then the qa phase failure output command exits non-zero
    And the qa phase failure output error names the failing step "ask"
    And the qa phase failure output error contains "boom: no such feature"

  Scenario: The calling run's logs contain the failing step output
    When the qa phase failure output user runs "cloche questions ask --file questions.yaml" from a workflow run
    Then the calling run's step log file for the failing step contains "boom: no such feature"
    And "cloche logs" for the calling run contains "boom: no such feature"
    And "cloche poll" for the calling run contains "boom: no such feature"

  Scenario: A successful phase leaves the error path untouched
    Given the qa module's phase scripts succeed
    When the qa phase failure output user runs "cloche questions ask --file questions.yaml" against the real daemon
    Then the qa phase failure output command exits 0
    And the qa phase failure output error does not contain "phase workflow failed"
