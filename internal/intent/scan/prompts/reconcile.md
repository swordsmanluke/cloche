# Reconcile Candidates Against Existing Requirements

Decide what to do with each candidate the extract step found: is it new, a
duplicate of something already tracked, evidence that an existing
requirement is now wrong, or not actually durable intent after all?

## Inputs

```bash
TEMP="$(cloche get temp_file_dir)"
```

- `$TEMP/candidates.json` — the extract step's output.
- `$CLOCHE_PROJECT_DIR/.cloche/intent/requirements/*.md` — every existing
  requirement (markdown with YAML frontmatter: `id`, `status`, `scope`,
  `confidence`, `user_edited`, `provenance`, plus the statement/rationale
  body). Read all of them, including `disabled` and `superseded` ones — you
  need their status to decide correctly.

## Decide one action per candidate

- **`create`** — the candidate describes intent not already covered by any
  existing requirement.
- **`merge`** — the candidate duplicates an existing **active** requirement.
  Nothing needs to change; you're just confirming it's already tracked.
- **`supersede`** — the candidate contradicts or meaningfully updates an
  existing **active** requirement with newer evidence (e.g. a later commit
  or doc reverses an earlier decision). This creates a new requirement and
  marks the old one superseded — the old one's text is never edited in
  place, only its status changes.
- **`drop`** — the candidate isn't durable intent after all (task-specific,
  code-derivable, transient), or it duplicates/contradicts a requirement
  that is **not active** (see hard rules below).

## Hard rules — a downstream validation step enforces these; get them right

1. **Never target a `disabled` or `superseded` requirement with `merge` or
   `supersede`.** A `disabled` requirement means a human turned it off —
   don't resurrect it by merging into it or superseding it; `drop` the
   candidate instead (optionally noting the disabled ID in `reason` for a
   human's benefit). A `superseded` requirement already has a current
   successor; if the candidate matches the same intent, target the
   successor instead.
2. **Never propose rewriting a `user_edited` requirement's own statement or
   scope.** `supersede` is the *only* action you may use against a
   `user_edited` requirement, and even then it does not touch the old
   file's text — it only flips its status and points at your new one. Do
   not choose `merge` for a `user_edited` requirement expecting to update
   its wording; use `supersede` if it's genuinely outdated, or `drop` the
   candidate if the existing text still holds.
3. **Nothing is ever deleted.** There is no delete action. The oldest
   history stays in git and in `superseded` files forever.

The applying step validates every action against these rules before writing
anything; if any action in the batch is invalid, none of them are applied
and the whole scan step fails. When unsure whether a target requirement
qualifies, re-read its `status` and `user_edited` fields rather than
guessing.

## Output

Write `$TEMP/reconcile.json`:

```json
{
  "actions": [
    { "action": "create", "statement": "...", "rationale": "...", "scope": {"level": "project"}, "hints": [...], "confidence": "high", "provenance": {...} },
    { "action": "merge", "existing_id": "req-a3f8", "reason": "duplicates existing requirement" },
    { "action": "supersede", "existing_id": "req-b91c", "statement": "...", "rationale": "...", "scope": {...}, "confidence": "high", "provenance": {...}, "reason": "commit abc123 reverses this" },
    { "action": "drop", "reason": "task-specific instruction, not durable intent" }
  ]
}
```

- `create` and `supersede` carry the same fields as a candidate in
  `candidates.json` (`statement`, `rationale`, `scope`, `hints`,
  `confidence`, `provenance`) — copy them from the candidate, editing only
  if reconciling against existing requirements gives you a clearer
  statement or scope.
- `merge`, `supersede`, and `drop`-with-a-target carry `existing_id`
  (the target requirement's `id` from its frontmatter).
- `reason` is optional but encouraged — it's the trail a human reviews
  later; always include it for `supersede` and for a `drop` that targets an
  existing requirement.
- One entry per candidate in `candidates.json`, in the same order.

## Results

- Emit `CLOCHE_RESULT:success` once `$TEMP/reconcile.json` is written.
- Emit `CLOCHE_RESULT:fail` if the candidates or existing requirements can't
  be read, or the output can't be written.
