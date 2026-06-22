Feature: Loop resume gate — daemon restart is gated on loop state

  As a Cloche operator
  I want the daemon to not silently resume interrupted runs when the loop is stopped
  So that I can rebuild or restart the daemon safely without unexpected runs firing

  # ─── L1: CLI surface ─────────────────────────────────────────────────────────

  Scenario: cloche loop quiesce prints confirmation with run count
    Given the orchestration loop is stopped
    And there are 3 resumable runs
    When the operator runs "cloche loop quiesce"
    Then the loop command succeeds
    And the loop command output contains "resumable runs parked"

  Scenario: cloche loop quiesce with no resumable runs reports zero
    Given the orchestration loop is stopped
    And there are no resumable runs
    When the operator runs "cloche loop quiesce"
    Then the loop command succeeds
    And the loop command output contains "0 resumable runs parked"

  Scenario: cloche loop stop --quiesce chains stop and quiesce in one command
    Given the orchestration loop is running
    When the operator runs "cloche loop stop --quiesce"
    Then the loop command succeeds
    And the orchestration loop is now stopped
    And the loop command output contains "resumable runs parked"

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

  Scenario: cloche loop quiesce marks runs as parked so they survive daemon restart
    Given the orchestration loop is stopped
    And there are 2 resumable runs
    When the operator runs "cloche loop quiesce"
    And the daemon is restarted
    Then no runs are automatically resumed

  Scenario: cloche loop status shows accurate parked-run count after quiesce
    Given the orchestration loop is stopped
    And there are 2 resumable runs
    When the operator runs "cloche loop quiesce"
    And the operator runs "cloche loop status"
    Then the loop command output contains "2"
    And the loop command output contains "resumable runs"

  Scenario: Normal vertical run completes end-to-end when loop is running
    Given the orchestration loop is running
    And a task is dispatched to the loop
    When the dispatched run completes
    Then the dispatched run status is successful
