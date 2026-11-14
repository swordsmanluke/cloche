Feature: DRA waits on owner-written host poll scripts
  A DRA can end its wake-up and be woken when something it waits on is ready.
  It names a poll the project owner wrote (or the built-in questions poll); it
  can never supply text for the host to run.

  Background:
    Given a DRA "qa-lead" whose project defines the polls "build-green" and "deploy-ready"

  # --- L1: CLI surface ---

  Scenario: Registering a wait prints an id and the wait is listed
    When the DRA runs "cloche dra wait build-green --arg branch=main --interval 5m --timeout 72h --note 'merge when green'"
    Then the command exits 0
    And the output contains a new wait id
    When the DRA runs "cloche dra waits"
    Then the output lists that wait with poll "build-green" and note "merge when green"

  Scenario: An unknown poll name is refused with the polls the DRA has
    When the DRA runs "cloche dra wait nonexistent --note 'x'"
    Then the command exits non-zero
    And the output says "unknown poll nonexistent"
    And the output lists the polls "build-green, deploy-ready"
    And no wait is registered

  Scenario: Cancelling a wait removes it
    Given the DRA has a pending wait on "build-green"
    When the DRA runs "cloche dra wait --cancel" with that wait's id
    Then the command exits 0
    And "cloche dra waits" no longer lists that wait

  Scenario: cloche dra ruling no longer exists
    When the DRA runs "cloche dra ruling Q14"
    Then the command exits non-zero
    And the output reports an unknown subcommand

  # --- L2: daemon registry, limits, events ---

  Scenario: Waiting on questions registers the built-in questions poll
    When the DRA runs "cloche dra wait questions --ids Q14,Q15 --note 'rulings needed'"
    Then the command exits 0
    And the wait is backed by the built-in questions poll for "Q14,Q15"

  Scenario Outline: A finished poll delivers one wait event in the next wake-up
    Given the DRA has a pending wait on "build-green" with note "merge when green"
    And the DRA has another event waiting in its inbox
    When the poll <outcome>
    And the DRA is next woken
    Then the wake-up message contains exactly one wait event
    And the wait event carries the note "merge when green", the result "<result>" and the poll's last output
    And the wait event is batched in the same message as the other event

    Examples:
      | outcome                   | result  |
      | prints a CLOCHE_RESULT    | ready   |
      | exits non-zero            | failed  |
      | passes its timeout        | timeout |

  Scenario: A poll that exits 0 with no marker is not yet ready
    Given the DRA has a pending wait on "build-green"
    When the poll exits 0 without a result marker
    Then the DRA receives no wait event
    And the wait is still pending

  Scenario: Wait limits are enforced
    Given the DRA has 20 pending waits
    When the DRA registers one more wait
    Then the registration is refused as over the 20 pending-wait limit
    When the DRA runs "cloche dra wait build-green --interval 30s --note 'x'"
    Then the registration is refused as under the 1 minute minimum interval

  Scenario: Wait arguments reach the poll as environment, never through a shell
    When the DRA registers a wait on "build-green" with the argument "branch=main; touch /tmp/pwned"
    And the poll runs
    Then the poll sees CLOCHE_WAIT_ARG_BRANCH exactly equal to "main; touch /tmp/pwned"
    And no command from the argument was executed

  # --- L3: persistence and real recorded run ---

  Scenario: A wait registered before a daemon restart still delivers its event
    Given the DRA has a pending wait on "build-green" with note "merge when green"
    When the daemon is restarted
    And the poll then prints a CLOCHE_RESULT
    Then the DRA's next wake-up contains the wait event for "merge when green"
    And the wait ran as a recorded host run named "dra-wait-build-green" with one poll step
