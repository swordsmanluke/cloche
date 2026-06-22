Feature: Per-step token metrics design doc
  As a cloche maintainer planning to implement per-step token metrics
  I want a complete design document at docs/plans/2026-05-28-step-token-metrics.md
  So that the schema, storage, query shapes, and CLI surface are fully specified before implementation begins

  # ─── L1: Schema and identity foundations ─────────────────────────────────────

  Scenario: Design doc exists at the expected path
    Given the step-token-metrics design doc
    Then the doc file exists

  Scenario: Design doc has a Step Identifier section that names the canonical key
    Given the step-token-metrics design doc
    Then the doc contains a "Step Identifier" section
    And the "Step Identifier" section names the canonical key fields

  Scenario: Design doc has a Schema section listing input and output token fields
    Given the step-token-metrics design doc
    Then the doc contains a "Schema" section
    And the "Schema" section lists "input_tokens" and "output_tokens" fields

  Scenario: Design doc addresses host-vs-container prompt step coverage
    Given the step-token-metrics design doc
    Then the doc contains a "Host vs Container" section

  # ─── L2: Query shapes and CLI surface ────────────────────────────────────────

  Scenario: Design doc specifies slice-by-step query with a concrete SQL example
    Given the step-token-metrics design doc
    Then the doc contains a slice-by-step query example

  Scenario: Design doc specifies aggregate-by-workflow query with a concrete SQL example
    Given the step-token-metrics design doc
    Then the doc contains an aggregate-by-workflow query example

  Scenario: Design doc specifies trend-over-time query with a concrete SQL example
    Given the step-token-metrics design doc
    Then the doc contains a trend-over-time query example

  Scenario: Design doc CLI section specifies cloche metrics or clo metric command forms
    Given the step-token-metrics design doc
    Then the doc contains a "CLI" section
    And the "CLI" section references "cloche metrics" or "clo metric"

  Scenario: Design doc storage section reconciles with the metrics-reporting proposal
    Given the step-token-metrics design doc
    Then the doc contains a "Storage" section
    And the "Storage" section references the metrics table

  Scenario: No open questions remain unresolved after both layers
    Given the step-token-metrics design doc
    Then the doc has no unresolved open questions
