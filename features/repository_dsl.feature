Feature: Repository DSL parsing
  As a developer using Cloche
  I want to declare Repository blocks in my .cloche files
  So that the system knows about my project's git repositories

  Scenario: Parse a single repository block
    Given a .cloche file containing:
      """
      repository "backend" {
        path    = "../backend"
        url     = "https://github.com/org/backend"
        default = true
      }

      workflow "develop" {
        step build {
          run     = "echo building"
          results = [success, fail]
        }
        build:success -> done
        build:fail    -> abort
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And the parsed file contains a repository named "backend" with path "../backend"
    And the parsed file contains a repository named "backend" with url "https://github.com/org/backend"
    And the parsed file contains a repository named "backend" marked as default

  Scenario: Parse multiple repository blocks
    Given a .cloche file containing:
      """
      repository "backend" {
        path    = "../backend"
        url     = "https://github.com/org/backend"
        default = true
      }

      repository "frontend" {
        path = "../frontend"
      }

      workflow "develop" {
        step build {
          run     = "echo building"
          results = [success, fail]
        }
        build:success -> done
        build:fail    -> abort
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And the parsed file contains a repository named "backend"
    And the parsed file contains a repository named "frontend"
    And the parsed file contains 2 repositories

  Scenario: Parse a step with a repository field
    Given a .cloche file containing:
      """
      repository "backend" {
        path    = "../backend"
        default = true
      }

      workflow "deploy" {
        step build {
          run        = "make build"
          repository = "backend"
          results    = [success, fail]
        }
        build:success -> done
        build:fail    -> abort
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And step "build" in workflow "deploy" has repository "backend"

  Scenario: A .cloche file with no repository blocks parses without error
    Given a .cloche file containing:
      """
      workflow "develop" {
        step run {
          run     = "echo hi"
          results = [success]
        }
        run:success -> done
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And the parsed file contains 0 repositories
