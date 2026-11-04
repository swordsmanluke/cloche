# Tasks

Two workflows turn Cloche into a worker. `list-tasks` finds the work. `main` does it.

![Your tracker feeds list-tasks. Each open task starts its own main. The loop repeats, with up to --max running at once.](img/tasks.svg)

## `list-tasks`: find the work

```
workflow list-tasks {
  host {}

  step list {
    run     = ".cloche/scripts/list.sh"
    results = [success, fail]
  }

  list:success -> done
  list:fail    -> abort
}
```

The script prints one JSON object per line. Each needs an `id` and a `status`.

```
{"id":"t1","status":"open","title":"Fix login"}
{"id":"t2","status":"open","title":"Write docs"}
```

## `main`: do the work

You wrote this on the [last page](4-the-dsl.md). Cloche starts it once per task and sets `CLOCHE_TASK_ID`. Close the task in `main`, or it comes back.

## Start the loop

Run one task to try it:

```
$ cloche loop once
Orchestration loop started (once mode).
Launched run pb3n-main (task t1). Loop stopped.
$ cloche tasks
ID                    STATUS        ASSIGNED    RUN                             TITLE
t1                    open          yes         pb3n-main                       Fix login
t2                    open          -                                           Write docs
```

Then let it run, three at a time:

```
$ cloche loop --max 3
Orchestration loop started (max_concurrent=3).
```

That's all of it: steps, wiring, `list-tasks`, `main`. You can now build a working Cloche project by hand.

---
**Next: [Let agents build them](6-let-agents-build-them.md) →**
