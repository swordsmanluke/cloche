Feature: Vertical workflow Phase 0.5 design-prep stage

  Before BDD test-plan authoring, the vertical workflow runs a design-prep
  stage that either skips to plan-feature (when an approved design doc is
  already cited) or drives the agent through write → PR → review → revise
  loop before proceeding.

  # ─── L1: DSL wiring and validation ──────────────────────────────────────────

  Scenario: vertical workflow with Phase 0.5 steps validates without error
    Given a vertical DSL file containing the Phase 0.5 check-design-needed step
    When the design-prep DSL file is validated
    Then no design-prep validation error is returned

  Scenario: claim:success is rewired to check-design-needed
    Given a vertical DSL file containing the Phase 0.5 check-design-needed step
    When the design-prep DSL file is validated
    Then no design-prep validation error is returned
    And in the design-prep workflow "claim" on "success" routes to "check-design-needed"

  Scenario: check-design-needed has-design path goes directly to plan-feature
    Given a vertical DSL file containing the Phase 0.5 check-design-needed step
    When the design-prep DSL file is validated
    Then no design-prep validation error is returned
    And in the design-prep workflow "check-design-needed" on "has-design" routes to "plan-feature"

  Scenario: check-design-needed needs-design path enters the design sub-stage
    Given a vertical DSL file containing the Phase 0.5 check-design-needed step
    When the design-prep DSL file is validated
    Then no design-prep validation error is returned
    And in the design-prep workflow "check-design-needed" on "needs-design" routes to "prepare-design-branch"

  Scenario: record-design:success routes to plan-feature
    Given a vertical DSL file containing the Phase 0.5 check-design-needed step
    When the design-prep DSL file is validated
    Then no design-prep validation error is returned
    And in the design-prep workflow "record-design" on "success" routes to "plan-feature"

  Scenario: address-design-feedback is declared with max_attempts 10
    Given a vertical DSL file containing the Phase 0.5 check-design-needed step
    When the design-prep DSL file is validated
    Then no design-prep validation error is returned
    And in the design-prep workflow step "address-design-feedback" has max_attempts of 10

  # ─── L2: Runtime skip-check and script behavior ──────────────────────────────

  Scenario: check-design-needed emits has-design when ticket cites an approved design doc
    Given a ticket description that references "docs/plans/2026-01-01-my-feature.md"
    And that design doc exists with status "Approved"
    When check-design-needed evaluates the ticket
    Then the check-design-needed result is "has-design"

  Scenario: check-design-needed emits needs-design when ticket has no cited design doc
    Given a ticket description with no docs/plans reference
    When check-design-needed evaluates the ticket
    Then the check-design-needed result is "needs-design"

  Scenario: open-design-pr PR body includes the Open Questions section from the design doc
    Given a design doc containing:
      """
      ## Open Questions for Reviewer

      1. Should the skip-check match partial Status values?
      2. Is max_attempts = 10 sufficient for address-design-feedback?
      """
    When the design PR is opened
    Then the PR body contains "Open Questions for Reviewer"
    And the PR body contains "Should the skip-check match partial Status values?"

  Scenario: finalize merges the design branch before test-plan when it exists
    Given a vertical stack where the design branch exists on the remote
    When finalize runs
    Then the merge order begins with the design branch
    And the design branch is merged before the test-plan branch
