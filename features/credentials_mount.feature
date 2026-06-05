Feature: credentials stay fresh across host token refreshes

  Long-running cloche attempts reuse a single Docker container across multiple
  sub-workflow steps. If the host's claude-code refreshes its OAuth token via
  atomic rename (write-temp + rename(2)) between steps, a single-file bind-mount
  goes stale: the container keeps reading the orphaned inode and the API returns 401.

  The fix replaces the single-file mount with a per-container staging directory and
  an fsnotify watcher. The watcher copies the updated credentials into the staging
  dir whenever the host path changes; because the staging dir is a directory mount,
  the rename is transparent to the container.

  Background:
    Given a temporary directory is used as the host Claude home

  # ─── L1: core fix (staged-dir mount + fsnotify watcher) ─────────────────────

  Scenario: container start uses a directory mount instead of a single-file mount
    Given a credentials file exists in the host Claude home
    When the docker runtime prepares start arguments for a new container
    Then the volume arg mounts the staging directory, not the credentials file path
    And the destination inside the container is "/home/agent/.claude/"

  Scenario: watcher propagates an atomic token refresh into the staging directory
    Given a credentials file exists in the host Claude home
    And the docker runtime has started a container with staged credentials
    When the credentials file on the host is replaced via atomic rename
    Then the staged copy contains the new credentials content

  Scenario: no credential volume is added when the credentials file is absent
    Given no credentials file exists in the host Claude home
    When the docker runtime prepares start arguments for a new container
    Then no volume arg references the credentials file

  # ─── L2: hardening (cleanup guarantees + error handling) ────────────────────

  Scenario: stopping a container removes its staging directory
    Given a credentials file exists in the host Claude home
    And the docker runtime has started a container with staged credentials
    When the container is stopped via the docker runtime
    Then the staging directory no longer exists on disk

  Scenario: a copy error during a credentials refresh is logged and does not abort the watcher
    Given a credentials file exists in the host Claude home
    And the docker runtime has started a container with staged credentials
    When the credentials file changes but writing to the staging directory is blocked
    Then the runtime continues running without returning an error
    And a warning is emitted to the log
