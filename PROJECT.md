# Next Features


## Orchestration

We need an orchestrator - a program whose task it is to grab the next "ready" ticket (from beads currently, but we'll need to 
wrap it in a general adapter for "task tracking" services, so that we can add later support for Jira, Linear, github issues, 
etc) and then generate an appropriate prompt (using the host's code agent) before kicking off `cloche run`. 

If `cloched`/`cloche-agent` are the workers in a factory, the orchestrator is the energy. It ensures the system continues to
run, delivering work to the system so long as any is available.

When started, it checks for any unblocked / ready work to begin.

`cloched` then kicks off the orchestrator (with the appropriate project context) whenever a task completes successfully.

## Indicator lights

If cloche is a factory, we need indicator lights to show when things are running well and when they are not.

## Web Dashboard

The main landing page dashboard should show each known project with indicator lights for status.

## Log contents

We need to capture _all_ output from each prompt and script, not just status changes. This will give us a log of the LLM's thoughts
and the scripts output to help reconstruct any issues post-facto.

## Container deletion

Don't auto-delete the containers from failed tasks. We may need them to investigate issues.
Show all kept containers in the web dashboard (and cli tool) with an option to delete them (cli: cloche delete <container name>)

