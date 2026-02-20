# CLOCHE

A system for grow-coding high quality applications.

# What is it

Cloche provides 
- containerized environments for coding agents to operate in isolation
- a workflow syntax for linking Agentic and Script-driven task nodes
- a focus on creating validated code
- self-evolving tooling that grows with your codebase in response to encountered errors

## Containers

Coding agents are powerful, but running them interactively turns the human into the bottleneck.

On the otherhand, running them in "yolo" mode is wildly risky.

To allow our agents to run in a balance of safety and speed, we must disrupt the Lethal Trifecta.

Firstly, we remove their access to the full filesystem - the docker containers have access only to their
own filespace - only copies are used - no file mounts. Environment variables are only those provided by the host
to the container. 

Second, network access is limited to allowlists to ensure your agents can have e.g. free access to library documentation or your
internal documentation, but not the internet at large!


## Workflows

see our [Workflows](docs/workflows.md) documentation!

## Validated code

Your agents are fast, but that speed shouldn't come at the cost of quality.

Keep your validation checks in the loop with your coding agent - go beyond unit tests!
- validate complexity measurements
- automated code review
- auto-split large commits into stacked branches, individually reviewed and validated to keep commits simple and clean
- custom script and agent support to add your own checks to the mix
- failures are tracked and classified for use in autogenerating future validation checks
- validation failure feedback triggers retries to automatically resolve issues before they reach CI
