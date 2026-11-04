# The DSL

A workflow file names its steps, then wires their results. Here is a full one.

![implement goes to test. test goes to done, or loops through fix. Failures go to abort.](img/workflow.svg)

```
workflow develop {
  container {
    image = "my-project:latest"
  }

  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run     = "make test"
    results = [success, fail]
  }

  step fix {
    prompt       = file(".cloche/prompts/fix.md")
    max_attempts = 2
    results      = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail    -> abort
  test:success      -> done
  test:fail         -> fix
  fix:success       -> test
  fix:fail          -> abort
  fix:give-up       -> abort
}
```

Every line of wiring is an arrow in the diagram. `max_attempts` caps the loop: on the third try, `fix` reports `give-up`.

## Add a `main`

`main` is the workflow Cloche runs for each task. It runs on the host and hands the work to the container.

```
workflow main {
  host {}

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  develop:success -> done
  develop:fail    -> abort
}
```

## Check it

`cloche validate` checks the files. `cloche workflow` draws the graph.

```
$ cloche validate
All configuration valid.
$ cloche workflow develop
workflow develop (container)

  ┌───────────┐
  │ implement │ agent  ◀ entry
  └───────────┘
    ├── success ──▶ test
    └── fail, timeout, token-limit ──▶ ABORT

  ┌───────────┐
  │ test      │ script
  └───────────┘
    ├── success ──▶ DONE
    ├── fail ──▶ fix
    └── timeout, token-limit ──▶ ABORT

  ┌───────────┐
  │ fix       │ agent
  └───────────┘
    ├── success ──▶ test
    └── fail, give-up, timeout, token-limit ──▶ ABORT
```

---
**Next: [Tasks](5-tasks.md) →**
