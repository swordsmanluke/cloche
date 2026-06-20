# Vertical workflow: check if a design doc is needed

Your job is to determine whether the feature already has an approved design document,
or whether Phase 0.5 (design prep) should run before implementation begins.

## Check procedure

1. Read the feature description:
   ```bash
   bd show "$CLOCHE_TASK_ID" --json | jq -r '.[0].description // empty'
   ```

2. Grep the description for references to `docs/plans/*.md` files:
   ```bash
   echo "<description>" | grep -oE 'docs/plans/[A-Za-z0-9._-]+\.md'
   ```

3. For each referenced path:
   - Check whether the file exists: `test -f <path>`
   - If it exists, check whether it contains the line `**Status:** Approved`:
     ```bash
     grep -q '^\*\*Status:\*\* Approved' <path>
     ```
   - If BOTH conditions hold, the feature has a valid approved design doc.

4. **If a valid approved doc is found:**
   ```bash
   cloche set design_doc_path "<path-to-doc>"
   ```
   Then output:
   ```
   CLOCHE_RESULT:has-design
   ```

5. **If no reference is found, no file exists, or no file has `**Status:** Approved`:**
   ```
   CLOCHE_RESULT:needs-design
   ```

## Rules

- A doc in `**Status:** Draft` does NOT count — it must be `**Status:** Approved`.
- If multiple `docs/plans/` paths are referenced, any one approved doc is sufficient.
- Do not create or modify any files.
- Do not run any git operations.
- Output **only** the `CLOCHE_RESULT:` line after your check completes.
