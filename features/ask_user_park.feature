Feature: ask_user over /mcp survives the client's ~60s limit and parks the run
  A Claude agent that calls the ask_user MCP tool must not see a timeout after
  about 60 seconds. The call blocks until a human replies or park_after elapses;
  then the run parks and resumes on "cloche threads reply".

  Scenario: A prompt reply within the client window is returned to the agent
    Given an agent step whose agent calls ask_user over /mcp
    When a human replies after 10 seconds
    Then the agent receives the reply as the tool result
    And the run is not parked

  Scenario: An unanswered ask outlasting 60 seconds does not time out for the agent
    Given an agent step whose agent calls ask_user over /mcp
    When nobody replies for 90 seconds
    Then the agent never receives a timeout result
    And the step does not fail

  Scenario: An unanswered ask is parked once park_after elapses
    Given an agent step whose agent calls ask_user over /mcp
    And park_after is 5 minutes
    When nobody replies for 5 minutes
    Then the run is parked
    And the help thread is in state "awaiting_user"

  Scenario: A parked run resumes when the thread is replied to
    Given an agent step whose agent calls ask_user over /mcp
    And the ask has been parked
    When a human runs "cloche threads reply" on the thread
    Then the run resumes with the reply as the answer to the ask

  Scenario: A reply arriving after 60 seconds but before park_after is still delivered
    Given an agent step whose agent calls ask_user over /mcp
    When a human replies after 120 seconds
    Then the agent receives the reply as the tool result
    And the run is not parked

  Scenario: The clo ask gRPC path is unaffected
    Given an agent step whose agent calls "clo ask" over gRPC
    When nobody replies until park_after elapses
    Then the run is parked
    And a reply via "cloche threads reply" resumes the run
