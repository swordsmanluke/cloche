Feature: Loop resume gate — daemon restart is gated on loop state

  As a Cloche operator
  I want the daemon to not silently resume interrupted runs when the loop is stopped
  So that I can rebuild or restart the daemon safely without unexpected runs firing

  # ─── L1: CLI surface ─────────────────────────────────────────────────────────

  Scenario: cloche loop stop --hard shuts down resumable runs and reports the count
    Given the orchestration loop is running
    And there are 3 resumable runs
    When the operator runs "cloche loop stop --hard"
    Then the loop command succeeds
    And the orchestration loop is now stopped
    And the loop command output contains "Shut down 3 running task"

  Scenario: cloche loop stop --hard with no resumable runs reports zero
    Given the orchestration loop is running
    And there are no resumable runs
    When the operator runs "cloche loop stop --hard"
    Then the loop command succeeds
    And the loop command output contains "Shut down 0 running task"

  Scenario: plain cloche loop stop halts the loop without shutting down runs
    Given the orchestration loop is running
    When the operator runs "cloche loop stop"
    Then the loop command succeeds
    And the orchestration loop is now stopped
    And the loop command output contains "Orchestration loop stopped"

  Scenario: cloche loop status shows a resumable runs field
    Given the cloche daemon is running
    When the operator runs "cloche loop status"
    Then the loop command succeeds
    And the loop command output contains "resumable runs"

  # ─── L2: Daemon gate ─────────────────────────────────────────────────────────

  Scenario: Daemon restart does not resume runs when loop is stopped
    Given the orchestration loop is stopped
    And a run was in-flight when the daemon last shut down
    When the daemon is restarted
    Then the in-flight run is not automatically resumed

  Scenario: Daemon restart resumes runs when loop is running
    Given the orchestration loop is running
    And a run was in-flight when the daemon last shut down
    When the daemon is restarted
    Then the in-flight run is automatically resumed

  Scenario: cloche loop stop --hard marks runs as parked so they survive daemon restart
    Given the orchestration loop is running
    And there are 2 resumable runs
    When the operator runs "cloche loop stop --hard"
    And the daemon is restarted
    Then no runs are automatically resumed

  Scenario: cloche loop status shows accurate parked-run count after a hard stop
    Given the orchestration loop is running
    And there are 2 resumable runs
    When the operator runs "cloche loop stop --hard"
    And the operator runs "cloche loop status"
    Then the loop command output contains "2"
    And the loop command output contains "resumable runs"

  Scenario: Normal vertical run completes end-to-end when loop is running
    Given the orchestration loop is running
    And a task is dispatched to the loop
    When the dispatched run completes
    Then the dispatched run status is successful
