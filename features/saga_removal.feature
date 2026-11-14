Feature: DRAs ask through cloche questions; Saga is gone from the container
  Once DRAs ask, await and read questions through `cloche questions`, the DRA
  image no longer ships a saga binary and locked containers no longer know a
  Saga address.

  Scenario: The DRA image contains no saga binary
    Given the DRA image build inputs (Dockerfile and make docker-dra)
    Then neither installs a saga binary into the image

  Scenario: The DRA image tests do not expect saga
    Given the DRA image tests
    Then none of them expect a saga binary to be present

  Scenario: The DRA image tests assert saga is absent
    Given the built DRA image
    When the image contents are inspected
    Then no saga binary is found on the image's PATH

  Scenario: A DRA in the image can still ask a question
    Given a DRA running in the saga-free image
    When it runs "cloche questions ask" with a question
    Then the command succeeds and prints a question id

  Scenario: A DRA in the image can await and read an answer
    Given a DRA running in the saga-free image that has asked a question
    When the question is answered and the DRA runs "cloche questions await" and "cloche questions read"
    Then both commands print the answer

  Scenario: Locked containers get no Saga egress route
    Given a locked container is configured
    Then its allowed environment and egress allowlist contain no Saga address

  Scenario: A configured Saga address is not passed through
    Given CLOCHE_SAGA_ADDR is set in the host environment
    When a locked container is configured
    Then the container receives no Saga address and no route to it

  Scenario: CLOCHE_SAGA_ADDR is read nowhere
    Given the repository source tree
    Then searching it for "CLOCHE_SAGA_ADDR" finds nothing

  Scenario: Docs no longer describe Saga access from the container
    Given docs/dras.md and docs/SAFETY.md
    Then neither describes Saga access from the container
    And docs/dras.md describes asking via cloche questions ask, await and read
