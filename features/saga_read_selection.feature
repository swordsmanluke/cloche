Feature: Saga module reads choice answers given by selection
  A choice question can be answered by picking options without typing text.
  Real "saga show --json" then returns an answer with "select" (1-based option
  positions) and no "text" key. read.sh must print the selected option labels
  and the text, and report "no answer yet" only when the chosen answer has neither.

  # --- L1: tests and stub saga that emit the real shapes ---

  Scenario: The stub saga can emit all four answer shapes
    When I inspect the stub saga used by the saga module tests
    Then it can emit selection-only, selection-plus-text, text-only and multi-select answers

  Scenario: Selection-only and multi-select cases fail against the unfixed read.sh
    When the new selection read tests run against the unfixed read.sh
    Then the selection cases fail and the text-only case passes

  # --- L2: read.sh prints labels and text ---

  Scenario: A selection-only answer is read back as its label
    Given a choice question with options "Yes" and "No" answered by selecting position 1 with no text
    When read.sh reads that choice question
    Then the read exits 0
    And the read output is exactly "Selected: Yes"

  Scenario: A selection plus text answer prints the label line then the text
    Given a choice question with options "Yes" and "No" answered by selecting position 1 with text "Go ahead"
    When read.sh reads that choice question
    Then the read output is "Selected: Yes" followed by the line "Go ahead"

  Scenario: A text-only answer prints just the text
    Given a choice question with options "Yes" and "No" answered with text "Maybe later" and no selection
    When read.sh reads that choice question
    Then the read output is exactly "Maybe later"

  Scenario: A multi-select answer lists every selected label
    Given a choice question with options "A", "B" and "C" answered by selecting positions 1 and 2
    When read.sh reads that choice question
    Then the read output is exactly "Selected: A, B"

  Scenario: An answer with neither selection nor text is still unanswered
    Given a choice question answered with an empty selection and no text
    When read.sh reads that choice question
    Then the read exits non-zero
    And the read reports the question has no answer yet

  Scenario: The module README documents selected labels in the read-answer row
    When I read the read-answer row of the saga module README
    Then it says the output has the selected option labels and the text
