Feature: Vertical workflow design-prep stage (Phase 0.5)
  As a cloche project maintainer running the vertical development workflow
  I want a Phase 0.5 design stage inserted before plan-feature
  So that significant features get a reviewed design doc before any implementation begins

  # ─── L1: Workflow DSL topology ─────────────────────────────────────────────

  Scenario: Phase 0.5 steps are present in the vertical workflow DSL
    Given the vertical workflow DSL
    When the vertical DSL is parsed
    Then no vertical DSL parse error is returned
    And the vertical workflow contains step "check-design-needed"
    And the vertical workflow contains step "prepare-design-branch"
    And the vertical workflow contains step "write-design"
    And the vertical workflow contains step "open-design-pr"
    And the vertical workflow contains step "poll-design-pr"
    And the vertical workflow contains step "address-design-feedback"
    And the vertical workflow contains step "record-design"

  Scenario: claim:success routes to check-design-needed
    Given the vertical workflow DSL
    When the vertical DSL is parsed
    Then no vertical DSL parse error is returned
    And in the vertical workflow the wire from "claim" on "success" goes to "check-design-needed"

  Scenario: check-design-needed:has-design skips the design sub-stage
    Given the vertical workflow DSL
    When the vertical DSL is parsed
    Then no vertical DSL parse error is returned
    And in the vertical workflow the wire from "check-design-needed" on "has-design" goes to "plan-feature"

  Scenario: check-design-needed:needs-design enters the design sub-stage
    Given the vertical workflow DSL
    When the vertical DSL is parsed
    Then no vertical DSL parse error is returned
    And in the vertical workflow the wire from "check-design-needed" on "needs-design" goes to "prepare-design-branch"

  Scenario: address-design-feedback has max_attempts of 10
    Given the vertical workflow DSL
    When the vertical DSL is parsed
    Then no vertical DSL parse error is returned
    And step "address-design-feedback" in the vertical workflow has max_attempts of 10

  # ─── L2: Skip-check classification ─────────────────────────────────────────

  Scenario: ticket citing an approved design doc takes the has-design path
    Given a design-check ticket description referencing "docs/plans/2026-05-31-vertical-design-prep-stage.md"
    And that design doc file exists and contains "**Status:** Approved"
    When the design-needed classifier runs
    Then the classifier result is "has-design"

  Scenario: ticket with no docs/plans reference takes the needs-design path
    Given a design-check ticket description with no docs/plans reference
    When the design-needed classifier runs
    Then the classifier result is "needs-design"

  Scenario: ticket citing a doc that lacks Status: Approved takes the needs-design path
    Given a design-check ticket description referencing "docs/plans/2026-05-31-draft-feature.md"
    And that design doc file exists but does not contain "**Status:** Approved"
    When the design-needed classifier runs
    Then the classifier result is "needs-design"

  # ─── L2: Script behaviors ───────────────────────────────────────────────────

  Scenario: vertical-finalize merges the design branch before test-plan
    Given a vertical feature named "my-feat"
    And the remote has a branch "vertical/my-feat/design"
    And the remote has a branch "vertical/my-feat/test-plan"
    When vertical-finalize runs for "my-feat"
    Then the design branch is merged before the test-plan branch

  Scenario: prepare-test-plan-branch bases off design_branch when set in KV
    Given a vertical feature named "my-feat"
    And the KV store has "design_branch" set to "vertical/my-feat/design"
    When the prepare-test-plan-branch script runs for "my-feat"
    Then the new test-plan branch is created off "vertical/my-feat/design"
