Feature: DRA waits on owner-written host poll scripts
  A DRA can end its wake-up and be woken when something it waits on is ready.
  It names a poll; it never supplies script text. Poll scripts are written by
  the project's owner and run on the host.

  Background:
    Given a DRA "qa" whose project owner has written the polls "build-done" and "deploy-ready"

  Scenario: Registering a wait on a named poll prints a wait id
    When the DRA runs "cloche dra wait build-done --arg branch=main --interval 5m --note 'main build'"
    Then the command succeeds and prints a wait id
    And "cloche dra waits" lists that wait with its poll name and note

  Scenario: Registering a wait on the built-in questions poll
    When the DRA runs "cloche dra wait questions --ids Q14,Q15 --note 'rulings'"
    Then the command succeeds and prints a wait id
    And "cloche dra waits" lists that wait as a questions wait on Q14 and Q15

  Scenario: An unknown poll name is refused with the DRA's available polls
    When the DRA runs "cloche dra wait no-such-poll --note 'x'"
    Then the command fails with "unknown poll no-such-poll"
    And the refusal lists the polls "build-done" and "deploy-ready"
    And no wait is registered

  Scenario: A pending wait can be cancelled
    Given the DRA has registered a wait on "build-done"
    When the DRA runs "cloche dra wait --cancel" with that wait's id
    Then "cloche dra waits" no longer lists that wait
    And no wait event is delivered for it

  Scenario: Ready poll delivers one wait event in the next wake-up
    Given the DRA has registered a wait on "build-done" with note "main build"
    When the poll script prints a CLOCHE_RESULT marker named "green"
    Then the DRA's next wake-up message contains exactly one wait event
    And that event carries the note "main build", the result name "green" and the poll's last output

  Scenario: A poll that exits 0 without a marker is not ready yet
    Given the DRA has registered a wait on "build-done"
    When the poll script exits 0 without printing a marker
    Then no wait event is delivered
    And the wait is still listed as pending

  Scenario: A failing poll and a timed-out wait each deliver a wait event
    Given the DRA has registered a wait on "build-done" and another on "deploy-ready" with a 1h timeout
    When the "build-done" poll script exits non-zero
    And the "deploy-ready" wait passes its timeout
    Then the DRA receives a wait event reporting failure for "build-done"
    And the DRA receives a wait event reporting timeout for "deploy-ready"

  Scenario: Wait events are batched with other inbox events
    Given the DRA has an unread inbox event of another kind
    And the DRA has a wait that becomes ready
    When the DRA is next woken
    Then both events arrive together in one wake-up message

  Scenario: Poll arguments reach the script as environment, never through a shell
    When the DRA registers a wait on "build-done" with the argument "branch=main; touch /tmp/pwned"
    And the poll runs
    Then the script sees CLOCHE_WAIT_ARG_BRANCH holding that exact text
    And the host did not execute "touch /tmp/pwned"

  Scenario: Wait limits are enforced
    Given the DRA already has 20 pending waits
    When the DRA registers one more wait
    Then registration is refused as over the 20 pending wait limit
    When the DRA registers a wait with interval "30s"
    Then registration is refused as under the 1 minute minimum interval
    When a wait is registered without a timeout
    Then it is recorded with a 72 hour timeout

  Scenario: Waits survive the end of the wake-up and a daemon restart
    Given the DRA has registered a wait on "build-done" and its wake-up has ended
    When the daemon restarts
    And the poll later reports ready
    Then the DRA receives the wait event in its next wake-up message

  Scenario: The ruling command and event kind are gone
    When the DRA runs "cloche dra ruling"
    Then the command is rejected as an unknown subcommand
    And "ruling" is not an event kind the inbox can deliver
