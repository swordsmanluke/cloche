# Patterns

Five shapes cover most workflows.

## Pass data between steps

![A script writes a key to the run's KV store. A later agent prompt reads it.](img/pattern-data.svg)

```
# script on the host; use clo set inside a container
cloche set test_summary "3 failed"

# in a prompt
Tests reported: {{ $test_summary }}
```

Values cap at 1 KB. For anything bigger, write a file and pass its path.

## Fix and retry

![test fails into fix. fix succeeds back into test. fix can also fail or give up, which goes to abort.](img/pattern-fix.svg)

```
step fix {
  prompt       = file(".cloche/prompts/fix.md")
  max_attempts = 2
  results      = [success, fail, give-up]
}
```

Out of attempts means `give-up`. Set a limit so the loop ends.

## Poll for feedback

![open PR leads to code-review, which polls every five minutes. approved goes to merge. fix goes to address feedback, then back to code-review.](img/pattern-poll.svg)

```
step code-review {
  poll     = "scripts/check-pr-review.sh"
  interval = "5m"
  timeout  = "48h"
  results  = [approved, fix]
}
```

Exit 0 with no result means "not yet." Print a result to move on.

## Router

![A route script reports bug, feature or docs. Each result starts a different sub-workflow.](img/pattern-router.svg)

A script picks the sub-workflow. Each target is a `workflow_name` step.

```
step route {
  run     = ".cloche/scripts/route.sh"
  results = [bug, feature, docs, fail]
}

route:bug     -> fix-bug
route:feature -> add-feature
route:docs    -> write-docs
route:fail    -> abort
```

The script decides however suits your project. A label on the task works well:

```
#!/bin/sh
echo "CLOCHE_RESULT:$(my-tracker label "$CLOCHE_TASK_ID")"
```

## Parallel paths

![build splits into lint and unit. collect all waits for both, then goes to done.](img/pattern-parallel.svg)

```
build:success -> lint
build:success -> unit
collect all(lint:success, unit:success) -> done
```

Wire one result to two steps to split. `collect all` waits for both. `collect any` waits for the first.

---
**Next:** [install Cloche](http://cloche.perfectlunacy.com/getting-started/installation/), or read the [full docs](http://cloche.perfectlunacy.com/learn/index.html).
