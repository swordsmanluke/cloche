Feature: Resume rebuild + preserve workspace design document
  As a cloche maintainer
  I want a complete design document for the resume-rebuild feature
  So that the implementing team has an unambiguous specification

  Background:
    Given the resume-rebuild design document exists

  # ─── L1: All open questions answered in a Draft doc ─────────────────────────

  Scenario: Design doc is promoted from pre-design to Draft
    Then the design document status is at least "Draft"

  Scenario: Workspace delta strategy question is answered
    Then the design document has a "Workspace delta strategy" section
    And the section contains a concrete choice between git branch and snapshot

  Scenario: Conflict resolution behavior is documented
    Then the design document has a "Conflict resolution" section
    And the section defines which files take precedence when conflicts occur

  Scenario: Multi-attempt selection default is specified
    Then the design document has a "Multi-attempt selection" section
    And the section states the default behavior when multiple failed attempts exist

  Scenario: Rebuild-preserve flag decision is documented
    Then the design document has a "Default vs opt-in" section
    And the section states whether rebuild-preserve is the default or requires an explicit flag

  # ─── L2: Final doc — no placeholders, concrete decisions, implementation notes

  Scenario: Design doc reaches Final status
    Then the design document status is "Final"

  Scenario: No placeholder language remains in the final doc
    Then the design document contains no unresolved placeholder text

  Scenario: Implementation notes reference key functions
    Then the design document has an "Implementation notes" section
    And the implementation notes reference "ResumeRunAsNewAttempt"
    And the implementation notes reference "CommitForResume"
