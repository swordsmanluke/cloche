Feature: Credential file stays current in long-running containers

  The cloche daemon stages host credentials into a per-container directory
  and uses fsnotify to re-copy the file whenever the host OAuth token is
  refreshed. This prevents 401 authentication errors that occur when a
  long-running attempt spans a host credential refresh (atomic rename).

  Background:
    Given the cloche daemon is running

  Scenario: Agent step authenticates on container startup
    Given the host has a valid credentials file
    And a cloche attempt is started for a project
    When an agent step executes inside the container
    Then the step completes without an authentication error

  Scenario: Container re-authenticates after host atomically refreshes credentials
    Given the host has a valid credentials file
    And a cloche attempt is in progress with at least one completed agent step
    When the host atomically replaces its credentials file via rename
    And another agent step executes inside the same container
    Then the step completes without an authentication error

  Scenario: Credential staging directory is removed when the container stops
    Given a cloche attempt has finished and the container has been stopped
    Then no cloche credential staging directories remain on the host

  Scenario: Watcher startup failure is reported as a visible warning
    Given the host credentials directory cannot be watched by fsnotify
    When the daemon starts a container for a new attempt
    Then a warning log entry identifies the affected container by ID
    And the daemon does not silently fall back to the old single-file bind-mount
