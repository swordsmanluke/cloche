# Repair the Candidate List

The candidate list you wrote did not pass validation. Do not re-read the
sources and do not change which candidates you found — fix the file's
format and field values only.

The validator said:

```
{{ $prev_output }}
```

The file is `$(cloche get temp_file_dir)/candidates.csv`. Its format is
exactly:

```csv
statement,rationale,scope_level,domains,hints,confidence,provenance_kind,provenance_ref
"Never bump the major version unless told to.","Batched manually.",domain,versioning,"is this a major bump;breaking change version bump",high,doc,CLAUDE.md#versioning
```

- `scope_level` is `project` or `domain`; `domains` lists domain names from
  `domains.yaml` separated by `;` (empty for `project`).
- `hints`: 2–5 phrases separated by `;`.
- `confidence`: `high`, `medium` or `low`. `provenance_kind`: `doc`,
  `transcript`, `prompt` or `commit`. `provenance_ref`: the ref exactly as
  given in the sources.
- Wrap a field in double quotes if it contains a comma, a double quote
  (written as `""`), or a line break.

Rewrite the file so every listed problem is gone, then:

- Emit `CLOCHE_RESULT:success` once the corrected file is written.
- Emit `CLOCHE_RESULT:fail` if the file cannot be written.
