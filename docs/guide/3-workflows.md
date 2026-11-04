# Workflows: the core of Cloche

A workflow is steps plus wiring. The step reports a result. The wiring picks the next step.

![implement goes to test. test goes to done, or loops through fix. Failures go to abort. done and abort sit together as the two ends of a run.](img/workflow.svg)

Agents can write anything. They can't change the path. `done` and `abort` end the run.

## Where a workflow runs

![A host workflow runs prepare-prompt, develop and merge on your machine. The develop step hands off to a container, which runs implement, test and fix, then returns a result and a branch.](img/env.svg)

| | Container | Host |
|---|---|---|
| Declared by | Default | A `host {}` block |
| Runs in | Docker, on a copy of your project | Your machine, with your permissions |
| Use for | Agent work | Orchestration: prepare, merge, close |

**Agents go in containers.** A host workflow can run an agent, with your full permissions. Save that for work that needs the host, like fixing a merge conflict.

---
**Next: [The DSL](4-the-dsl.md) →**
