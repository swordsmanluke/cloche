Feature: credentials bind-mount inode stability

  Docker file bind-mounts pin to the source inode at container-creation time.
  When the host atomically renames ~/.claude/.credentials.json (the standard safe-write
  pattern used by claude-code token refresh), the container mount still points at the
  orphaned inode, causing 401 auth failures in long-running containers.

  Cloche fixes this by staging a per-container ~/.claude/ directory and using an
  fsnotify watcher to propagate credential refreshes into the staging copy.

  # ─── L1: staging directory + fsnotify watcher ───────────────────────────────

  Scenario: runtime mounts a directory not a single credentials file
    Given a docker runtime configured with a credentials source directory
    When a container is started
    Then the docker bind-mount argument references a directory not a single file
    And the container path mounted is "/home/agent/.claude/"

  Scenario: updated credentials propagate into the container after host atomic rename
    Given a docker runtime configured with a credentials source directory
    And a container has been started
    When the host atomically replaces ".credentials.json" with new content
    Then the staging directory reflects the new credentials content within 2 seconds

  Scenario: two simultaneously running containers have independent staging directories
    Given a docker runtime configured with a credentials source directory
    When two containers are started concurrently
    Then each container has a distinct staging directory path

  # ─── L2: hardening ────────────────────────────────────────────────────────────

  Scenario: staging directory is cleaned up when the container stops
    Given a docker runtime configured with a credentials source directory
    And a container has been started
    When the container is stopped
    Then the container's staging directory no longer exists on the host

  Scenario: all staging directories are removed after multiple containers stop
    Given a docker runtime configured with a credentials source directory
    When three containers are started and then stopped
    Then no cloche credential staging directories remain on the host

  Scenario: fsnotify watcher failure emits a warning but does not prevent container start
    Given a docker runtime where the credentials source directory cannot be watched
    When a container is started
    Then the container start succeeds
    And a warning is logged that includes the container ID
