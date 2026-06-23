Feature: Run-state per-step view design document
  As a developer implementing the run-state web UI
  I want a complete design document at docs/plans/2026-05-28-run-state-step-view.md
  So that I can implement the flat step table without needing follow-up clarification

  # ─── L1: Data model ──────────────────────────────────────────────────────────

  Scenario: Design doc exists at the expected path
    Given the run-state step-view design doc
    Then the design doc file exists

  Scenario: Row schema defines all required columns
    Given the run-state step-view design doc
    Then the design doc defines a row schema section
    And the row schema includes a fully-qualified step name column
    And the row schema includes a result column
    And the row schema includes an ISO-8601 started column
    And the row schema includes a duration column
    And the row schema includes a workflow-segment tag column for color grouping

  Scenario: Canonical step identifier format is specified and consistent
    Given the run-state step-view design doc
    Then the design doc specifies the canonical step identifier format
    And the step identifier format is consistent with the step-token-metrics design doc

  Scenario: Data source endpoint is named with response shape
    Given the run-state step-view design doc
    Then the design doc names a data source endpoint
    And the design doc describes how subworkflow steps are surfaced in the response

  # ─── L2: UI presentation ─────────────────────────────────────────────────────

  Scenario: Color assignment section names CSS variables from the cleanup palette
    Given the run-state step-view design doc
    Then the design doc has a color assignment section
    And the color assignment section references CSS variables from the web-ui-cleanup palette
    And the color assignment section states a stability rule

  Scenario: Step ordering strategy is stated with rationale
    Given the run-state step-view design doc
    Then the design doc has a step ordering section
    And the step ordering section states the chosen order
    And the step ordering section provides a rationale for the choice

  Scenario: Nesting-depth handling is described
    Given the run-state step-view design doc
    Then the design doc has a nesting strategy section
    And the nesting strategy section states whether depth is shown by color, indent, or both
    And the nesting strategy section describes long fully-qualified name handling

  Scenario: All original open questions are resolved
    Given the run-state step-view design doc
    Then the design doc has no unresolved open questions about identifier canonicalization
    And the design doc has no unresolved open questions about the data source shape
