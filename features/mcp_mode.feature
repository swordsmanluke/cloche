Feature: MCP mode — inverted-control interactive agent execution
  As a workflow author
  I want to run prompt steps against a long-lived interactive Claude session
  So that I can use shared conversation context and interactive tooling across an entire run

  # ─── Mode selection ──────────────────────────────────────────────────────────

  Scenario: A project declares MCP mode and port in its config
    Given a project config with mcp_mode enabled and mcp_port set to 9200
    When the project config is loaded
    Then no MCP config error is returned
    And the project is configured for MCP mode on port 9200

  Scenario: Two MCP projects on the same daemon use different ports
    Given a daemon with project "alpha" on mcp_port 9200 and project "beta" on mcp_port 9201
    When both projects are running MCP sessions concurrently
    Then the daemon routes each session by port without collision

  # ─── Workflow step syntax ─────────────────────────────────────────────────────

  Scenario: A workflow step forwards a prompt to the agent via "clo mcp"
    Given an MCP-mode project with an active interactive agent session
    When a workflow step executes "clo mcp" with a prompt body
    Then the daemon caches the prompt until the MCP client requests it

  Scenario: The agent receives the cached prompt via the cloche MCP next_step tool
    Given the daemon has cached a prompt from a "clo mcp" step
    When the interactive agent calls the cloche MCP next_step tool
    Then the agent receives the prompt text for the pending step

  # ─── Interactive agent startup ────────────────────────────────────────────────

  Scenario: Starting a workflow run under an MCP project launches an interactive agent
    Given an MCP-mode project with a workflow containing a prompt step
    When the daemon starts the workflow run
    Then an interactive Claude session is started inside the container
    And the container's AGENTS.md instructs the agent to loop on next_step requests

  Scenario: The interactive agent's MCP is configured to connect to the daemon port
    Given the daemon started an interactive agent for an MCP project on port 9200
    When the agent initialises its MCP connection
    Then the MCP client connects to the daemon on port 9200

  # ─── Control-flow: next_step and submit-result ───────────────────────────────

  Scenario: The agent advances the run by submitting a named result to the daemon
    Given the interactive agent has received a prompt step with wires "success" and "fail"
    When the agent calls the cloche MCP submit-result tool with named result "success"
    Then the run advances to the step wired to "success"

  Scenario: The agent loops back to next_step after submitting a result
    Given the agent has just submitted a result for a completed prompt step
    When the agent calls the cloche MCP next_step tool again
    Then the agent receives the next pending prompt step

  # ─── Script steps are unaffected ─────────────────────────────────────────────

  Scenario: Script steps in an MCP-mode workflow run directly without agent interaction
    Given an MCP-mode run reaches a script step
    When the script step executes
    Then the script step completes without a next_step or submit-result call

  # ─── Conversation continuity ─────────────────────────────────────────────────

  Scenario: Subsequent next_step calls in the same session carry accumulated context
    Given an MCP session is active with two sequential prompt steps
    And the agent has submitted the first prompt step with result "done"
    When the agent calls the cloche MCP next_step tool for the second step
    Then the agent receives the second prompt within the same conversation context

  # ─── Result protocol replaces CLOCHE_RESULT stdout marker ────────────────────

  Scenario: A result submitted via submit-result wires the workflow without stdout markers
    Given an MCP session has a prompt step pending
    When the agent calls the cloche MCP submit-result tool with named result "fail"
    Then the run follows the "fail" wire
    And no CLOCHE_RESULT marker appears in the captured output
