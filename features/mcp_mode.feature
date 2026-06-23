Feature: MCP mode — inverted-control interactive agent execution

  When a project or run is configured for MCP mode, Cloche exposes an MCP server
  that an interactive Claude client connects to. Prompt steps are dispatched to
  the waiting client instead of launching a headless `claude -p` process. Script
  steps are unaffected. Results, token usage, and streamed logs flow back through
  the MCP submit-result tool.

  # ─── L1: Configuration ───────────────────────────────────────────────────────

  Scenario: MCP mode can be enabled in project config
    Given a config.toml with agent mode "mcp"
    When the mcp config is loaded
    Then the loaded config has agent mode "mcp"

  Scenario: default agent mode is "prompt" when not configured
    Given a config.toml with no agent mode setting
    When the mcp config is loaded
    Then the loaded config has agent mode "prompt"

  # ─── L2: MCP server tool surface ─────────────────────────────────────────────

  Scenario: a client calls init and receives a session token
    Given the daemon is running in MCP mode for run "run-001"
    When the MCP client calls "init" for run "run-001"
    Then the init response contains a non-empty session token

  Scenario: a client calls next and receives a pending prompt
    Given an MCP session for run "run-001" has a pending prompt step with prompt "Summarize the diff"
    When the MCP client calls "next" with the session token
    Then the next response contains the prompt text "Summarize the diff"

  Scenario: a client submits a result and the step is completed
    Given an MCP session for run "run-001" is waiting for step "analyze" to complete
    When the MCP client calls "submit-result" with result "success", output "Done.", and 200 output tokens
    Then step "analyze" in run "run-001" is marked complete with result "success"
    And 200 output tokens are recorded for step "analyze" in run "run-001"

  # ─── L3: End-to-end execution ─────────────────────────────────────────────────

  Scenario: prompt step is dispatched via MCP instead of launching claude -p
    Given a project configured for MCP mode
    And a workflow "deploy" with one prompt step "analyze"
    When the MCP client connects and submits result "success" for step "analyze"
    Then workflow run "deploy" completes with result "success"
    And the headless claude-p executor is never invoked

  Scenario: script steps in an MCP-mode workflow bypass MCP dispatch
    Given a project configured for MCP mode
    And a workflow "build" with one script step "compile" running "echo ok"
    When the workflow "build" executes
    Then step "compile" completes without MCP dispatch
    And workflow run "build" completes with result "success"

  Scenario: multiple prompt steps share the same MCP session
    Given a project configured for MCP mode
    And a workflow "pipeline" with two sequential prompt steps "analyze" and "summarize"
    When the MCP client connects and handles both prompt steps
    Then both steps complete using the same MCP session token

  Scenario: workflow fails when no MCP client connects before the deadline
    Given a project configured for MCP mode with a connect timeout of 1 second
    And a workflow "check" with one prompt step "review"
    When the workflow "check" executes with no MCP client connecting
    Then workflow run "check" is marked failed
    And the failure reason mentions "MCP client did not connect"
