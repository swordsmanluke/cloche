Feature: Credential refresh in long-running containers

  The Docker runtime bind-mounts ~/.claude/.credentials.json into containers for
  agent authentication. A single-file bind-mount pins the source inode at mount
  time, so when the host refreshes tokens via atomic rename (write-temp + rename(2))
  the container keeps reading the orphaned inode and gets 401s.

  This feature replaces the single-file mount with a per-container staging directory
  plus an fsnotify watcher that re-copies credentials on host changes, ensuring the
  container always reads through the dentry and sees the latest token.

  Background:
    Given a temporary host .claude directory with a credentials file containing "token-v1"

  # ── L1: staging mount + fsnotify watcher ─────────────────────────────────────

  Scenario: Container receives current credentials at startup
    When a container is started via the runtime
    Then the container can read the credentials file
    And the credentials content seen by the container is "token-v1"

  Scenario: Credentials propagate to the container after atomic rename on the host
    Given a container is running via the runtime
    When the host atomically replaces the credentials file with content "token-v2"
    Then within 2 seconds the container reads credentials content "token-v2"

  Scenario: Multiple successive credential rotations all propagate
    Given a container is running via the runtime
    When the host atomically replaces the credentials file with content "token-v2"
    And the host atomically replaces the credentials file with content "token-v3"
    Then within 2 seconds the container reads credentials content "token-v3"

  Scenario: settings.json is also staged alongside credentials at startup
    Given the host .claude directory also contains a settings.json file
    When a container is started via the runtime
    Then the container's staged directory contains a settings.json file

  # ── L2: cleanup, error handling, integration ─────────────────────────────────

  Scenario: Staging directory is removed when the container stops
    Given a container is running via the runtime
    When the container is stopped
    Then no staging directory matching "cloche-claude-*" remains under the system temp dir

  Scenario: No staging directories accumulate across multiple container lifecycles
    When 3 containers are started and stopped sequentially via the runtime
    Then no staging directories matching "cloche-claude-*" remain under the system temp dir

  Scenario: Watcher failure is logged with the container ID and does not crash the daemon
    Given the host .claude directory is not watchable
    When a container is started via the runtime
    Then a warning is logged that mentions the container ID

  Scenario: Staging directory is removed even if the container stops abnormally
    Given a container is running via the runtime
    When the container terminates without a clean stop call
    Then no staging directory matching "cloche-claude-*" remains under the system temp dir
