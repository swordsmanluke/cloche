Feature: Repository CLI surface
  As a developer using Cloche
  I want to view project repositories via the cloche CLI
  So that I can inspect my multi-repo project configuration

  Background:
    Given the daemon is running against a test project directory

  # ─── cloche project display ──────────────────────────────────────────────────

  Scenario: cloche project shows a Repositories section when repositories are declared in config.toml
    Given the project's config.toml declares:
      """
      [[repositories]]
      name = "backend"
      path = "./repos/backend"
      default = true

      [[repositories]]
      name = "frontend"
      path = "./repos/frontend"
      """
    When the user runs "cloche project"
    Then the command succeeds
    And the output contains "Repositories:"
    And the output contains "backend"
    And the output contains "./repos/backend"
    And the output contains "frontend"

  # ─── cloche project repos subcommands ───────────────────────────────────────

  Scenario: cloche project repos list shows repositories from config.toml
    Given the project's config.toml has a repository entry named "backend" with path "./repos/backend"
    When the user runs "cloche project repos list"
    Then the command succeeds
    And the output contains "backend"
    And the output contains "./repos/backend"

  # ─── Backward compatibility ──────────────────────────────────────────────────

  Scenario: cloche project shows a deprecation warning and migration instructions for a legacy project with no repository config
    Given the project's config.toml has no repository entries
    When the user runs "cloche project"
    Then the command succeeds
    And the output does not contain "Repositories:"
    And the output contains a deprecation warning about missing repository configuration
    And the output contains migration instructions for adding repository configuration

  Scenario: Existing single-repo project works without config changes
    Given the project's config.toml has no repository entries
    And the project has no stored repositories
    When the user runs "cloche project"
    Then the command succeeds
    And the output contains "Project:"
    And the output contains "Directory:"

  Scenario: Upgrading the database auto-seeds a default repository from the project root
    Given a project database that has been freshly migrated with no repository rows
    When the repositories store is first accessed for that project
    Then exactly 1 repository is seeded automatically
    And the seeded repository is marked as default
    And the seeded repository has path equal to the project root directory
