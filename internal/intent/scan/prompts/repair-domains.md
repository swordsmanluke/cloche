# Repair the Domain List

The domain list you wrote did not pass validation. Do not re-survey the
project and do not change which domains you proposed — fix the file's
format only.

The validator said:

```
{{ $prev_output }}
```

The file is `$(cloche get temp_file_dir)/domains.csv`. Its format:

```csv
name,description,paths
workflow-dsl,"The .cloche workflow DSL — parser, validation, step types, wiring.",internal/dsl/**;docs/workflows.md
```

- Exactly three columns; the header line must be `name,description,paths`.
- `name` is kebab-case (`[a-z0-9-]`) and unique; `description` is non-empty;
  `paths` has at least one glob, several separated by `;`.
- Wrap a field in double quotes if it contains a comma, a double quote
  (written as `""`), or a line break.

Rewrite the file so every listed problem is gone, then:

- Emit `CLOCHE_RESULT:success` once the corrected file is written.
- Emit `CLOCHE_RESULT:fail` if the file cannot be written.
