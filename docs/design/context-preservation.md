# Design: Context Preservation Across Iteration Cycles

## Original Feedback

> Iteration cycles with cloche are costly. Every time I
> update PR comments and ask it to address them, it spins up a new
> container with a fresh context so the LLM must spend a bunch of
> tokens rereading files that it read in the last iteration of the
> PR. It'd be nice if there was a way to mitigate this cost by
> preserving some context from the previous work on that same PR.

### Notes

- Cloche console?
- What about multi-stage workflows that preserve the container(s)
  until the task is closed?
  - No cleanup step, just named containers, to ensure they stick around
  - When main "completes", don't close the task, leave it in 'review' or whatever
  - A branch in 'list-tasks' that checks review comments, then kicks off a workflow
    for addressing them *inside* the previously existing container?
  - What about a 'loop' type workflow which defines a script and a downstream workflow?
    Then the 'loop' workflows are registered with the project. The main loop becomes
    'look for task, assign the task to main'. Loops differ in that they don't wait for
    their sub-workflow to complete. They are a polling primitive that looks for "Work",
    then assigns it to a workflow.
