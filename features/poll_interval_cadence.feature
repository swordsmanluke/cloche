Feature: Poll steps fire at their configured interval
  A poll step declared with interval = "30s" must run its poll script about
  every 30 seconds, regardless of how often the orchestration loop ticks.
  Intervals that cannot be honoured are rejected or warned about by the
  validator instead of being silently stretched.

  # L1 = validator floor + cadence test (fake clock, stubbed poll runner)
  # L2 = polls driven from their own timer in the poll coordinator

  Scenario: Interval below the documented floor is flagged by the validator
    Given a workflow whose wait step polls with interval "100ms"
    When the workflow is validated
    Then the validator reports the interval as below the minimum poll interval
    And the report names the step "wait" and states the minimum

  Scenario: Interval at or above the floor validates cleanly
    Given a workflow whose wait step polls with interval "30s"
    When the workflow is validated
    Then the validator reports no poll interval problem

  Scenario: Poll fires at its interval when the loop ticks slower
    Given a poll step with interval "30s" and timeout "24h"
    And the orchestration loop ticks every "60s"
    When 5 minutes of fake time pass
    Then the poll script ran about every 30 seconds
    And no gap between consecutive runs exceeds 30 seconds plus the tolerance

  Scenario: Poll fires at its interval when the loop ticks faster
    Given a poll step with interval "30s" and timeout "24h"
    And the orchestration loop ticks every "5s"
    When 5 minutes of fake time pass
    Then the poll script ran about every 30 seconds
    And the poll script was not run more often than once per interval

  Scenario: Two polls with different intervals keep independent cadences
    Given a poll step "fast" with interval "30s" and a poll step "slow" with interval "90s"
    When 6 minutes of fake time pass
    Then "fast" ran about every 30 seconds
    And "slow" ran about every 90 seconds

  Scenario: Poll timeout is still honoured
    Given a poll step with interval "30s" and timeout "2m"
    And the poll script never reports ready
    When 5 minutes of fake time pass
    Then the step ends as timed out after about 2 minutes
    And the poll script is not run again after the timeout

  Scenario: A slow poll script does not cause overlapping runs
    Given a poll step with interval "30s" and timeout "24h"
    And each poll script run takes 45 seconds
    When 5 minutes of fake time pass
    Then no two runs of the poll script overlap

  Scenario: A standalone poll step honours its interval too
    Given a standalone poll step with interval "30s" and timeout "24h"
    When 5 minutes of fake time pass
    Then the poll script ran about every 30 seconds
