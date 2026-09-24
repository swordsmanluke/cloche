# Repair the Reconcile Decisions

Your reconcile decisions did not pass validation. Do not redo the
reconciliation from scratch — fix only the problems listed, in the file's
format or in the specific rows named.

The validator said:

```
{{ $prev_output }}
```

The file is `$(cloche get temp_file_dir)/reconcile.csv`. Its format is
exactly:

```csv
candidate,action,existing_id,reason,statement,rationale,scope_level,domains,hints,confidence,provenance_kind,provenance_ref
1,create,,,,,,,,,,
2,merge,req-a3f8,duplicates existing requirement,,,,,,,,
3,supersede,req-b91c,commit abc123 reverses this,"New statement",,,,,,,
4,drop,,task-specific instruction,,,,,,,,
```

- Exactly one row per candidate in `candidates.json`, in order; `candidate`
  is the 1-based index.
- `action` is `create`, `merge`, `supersede` or `drop`; `merge` and
  `supersede` need `existing_id` (a `req-xxxx` id from the requirements
  directory).
- For `create`/`supersede`, leave a field blank to keep the candidate's own
  value; fill it only to change it.
- If a row was rejected for a hard rule (targeting a `disabled` or
  `superseded` requirement, or `merge` into a `user_edited` one), change that
  row's action — `drop` it, or `supersede` the current successor — rather
  than the target's file.
- Wrap a field in double quotes if it contains a comma, a double quote
  (written as `""`), or a line break.

Rewrite the file so every listed problem is gone, then:

- Emit `CLOCHE_RESULT:success` once the corrected file is written.
- Emit `CLOCHE_RESULT:fail` if the file cannot be written.
