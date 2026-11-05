# Workflows: the core of cloche

A Workflow is a name, some config, then the list of Steps plus wiring. You've seen wiring already in [Steps](2-steps.md)
```
test:success -> done
test:fail    -> fix
```

![implement goes to test. test goes to done, or loops through fix. Failures go to abort. done and abort sit together as the two ends of a run.](img/workflow.svg)

Steps and Wiring define the behavior of the Workflow. This is how you make your software factory predictable even while using LLMs. 

## Isolation

Agents are great but very powerful. A rogue agent can leak data or wreck your machine. Cloche Workflows let the user set the isolation context
for their agents. Workflows can be run on the **host** or within a **container**. cloche makes it simple to run your agentic commands within
a containerized environment - by default, Workflows will run in a container. Or, use a `host{}` config block to set up work that needs to 
run with access to your host environment.

Use the Workflow Step type to handoff control across the host/container boundary.

For instance:

![A host workflow runs prepare-prompt, develop and merge on your machine. The develop step hands off to a container, which runs implement, test and fix, then returns a result and a branch.](img/env.svg)

| | Container | Host |
|---|---|---|
| Declared by | Default | A `host {}` block |
| Runs in | Docker, on a copy of your project | Your machine, with your permissions |
| Use for | Agent work | Orchestration: prepare, merge, close |

**Agents should go in containers.** A host workflow _can_ run agent steps, which will run with your standard permissions and access to your 
machine - so use it sparingly, when you really do need the escape hatch.

---
**Next: [The DSL](4-the-dsl.md) →**
