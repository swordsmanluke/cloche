Feature: DRAs ask through cloche questions; Saga is gone from the container
  Once DRAs ask, await and read questions through `cloche questions`, the DRA
  image no longer ships a saga binary and locked containers no longer get a
  Saga egress route.

  Scenario: The DRA Dockerfile does not install saga
    When the DRA image build files are inspected
    Then docker/cloche-dra/Dockerfile and the make docker-dra copy step install no saga binary

  Scenario: The built DRA image contains no saga binary
    Given the DRA image is built with make docker-dra
    Then no saga binary exists anywhere on the image's PATH

  Scenario: The DRA image tests do not expect saga
    When the DRA image tests are read
    Then none of them expects a saga binary to be present

  Scenario: A DRA in the image can still ask, await and read a question
    Given a DRA running in the DRA image
    When the DRA runs "cloche questions ask" with a question
    And the DRA runs "cloche questions await" on the returned id
    And the DRA runs "cloche questions read" on the returned id
    Then each command succeeds and the answer is readable by the DRA

  Scenario: A locked container gets no Saga egress route
    Given a locked DRA container is started
    Then its egress allowlist contains no Saga address
    And CLOCHE_SAGA_ADDR is not set in the container environment

  Scenario: Setting CLOCHE_SAGA_ADDR on the host has no effect on a locked container
    Given CLOCHE_SAGA_ADDR is set in the host environment
    When a locked DRA container is started
    Then the egress allowlist is the same as when it is unset

  Scenario: CLOCHE_SAGA_ADDR is read nowhere in the repository
    When the repository is searched for "CLOCHE_SAGA_ADDR"
    Then there are no matches

  Scenario: The DRA and safety docs no longer describe Saga access from the container
    When docs/dras.md and docs/SAFETY.md are read
    Then neither describes Saga access or a Saga egress route from the container
    And docs/dras.md describes asking via cloche questions ask, await and read
