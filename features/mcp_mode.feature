Feature: MCP mode design document (inverted-control interactive agent execution)
  As a cloche contributor
  I want a complete design doc for MCP mode at docs/plans/2026-05-28-mcp-mode.md
  So that implementation can proceed from a reviewed, unambiguous specification

  # ─── L1: Core architectural decisions ────────────────────────────────────────

  Scenario: Design doc exists at the expected path
    Given the MCP mode design doc
    When the design doc is read
    Then no read error is returned

  Scenario: Server hosting decision is documented with rationale
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc contains a server hosting decision

  Scenario: MCP tool surface defines init, poll, and submit-result
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc defines the "init" MCP tool
    And the design doc defines the "poll" MCP tool
    And the design doc defines the "submit-result" MCP tool

  Scenario: Result protocol replacing CLOCHE_RESULT stdout marker is defined
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc defines the result protocol replacing CLOCHE_RESULT

  # ─── L2: Full design completeness ────────────────────────────────────────────

  Scenario: Concurrency model is addressed
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc addresses the concurrency model

  Scenario: Conversation continuity across prompt steps is addressed
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc addresses conversation continuity

  Scenario: Mode selection per project and per run is defined
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc defines how a user opts in to MCP mode

  Scenario: Token usage and log streaming flow is specified
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc specifies token and log flow back to the engine

  Scenario: Human-in-the-loop integration is covered
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc covers human-in-the-loop integration

  Scenario: Design doc status is promoted beyond the pre-design Captured state
    Given the MCP mode design doc
    When the design doc is read
    Then the design doc status is not "Captured (pre-design)"
