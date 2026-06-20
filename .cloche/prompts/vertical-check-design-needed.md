# Check design needed (stub)

You are checking whether the feature task already has an approved design doc.

Look at the task description. If it references a file under `docs/plans/` that
exists and contains `**Status:** Approved`, output:

```
CLOCHE_RESULT:has-design
```

Otherwise output:

```
CLOCHE_RESULT:needs-design
```

<!-- TODO: L2 — replace this stub with a real classifier that reads the task
description from KV (task_description) and checks the referenced doc file. -->

CLOCHE_RESULT:needs-design
