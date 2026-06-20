# Vertical workflow: address design PR feedback

The design PR has received feedback. Read the comments, update the design doc to
address them, and push. Unanswered open questions must remain visible for the next
review cycle.

## Read the current PR and doc

```bash
pr_number=$(cloche get current_pr_number)
doc_path=$(cloche get design_doc_path)

# Review summaries
gh pr view "$pr_number" --json reviews \
  --jq '.reviews[] | select(.body != "" and .body != null) | "## \(.author.login) — \(.state)\n\n\(.body)\n"'

# Inline review comments
gh api "repos/{owner}/{repo}/pulls/$pr_number/comments" \
  --jq '.[] | "## \(.user.login) on \(.path):\(.line // .original_line)\n\n> \(.body)\n"'

# PR-level comments
gh pr view "$pr_number" --json comments \
  --jq '.comments[] | "## \(.author.login)\n\n\(.body)\n"'

# The current design doc
cat "$doc_path"
```

## Address each comment

For each piece of feedback:

1. **If the reviewer answered an Open Question:** incorporate the answer into the
   relevant section of the doc and **remove that question** from
   `## Open Questions for Reviewer`.

2. **If the reviewer requested a change to the design:** make the change to the
   appropriate section.

3. **If the reviewer raised a new concern you cannot resolve alone:** add it to
   `## Open Questions for Reviewer` as a new numbered entry.

4. **If feedback is contradictory or unclear:** add a PR comment explaining what
   you understood and what you chose to do, then proceed with the most reasonable
   interpretation.

**Do not remove unanswered questions from `## Open Questions for Reviewer`.** Only
remove a question when the reviewer (or you, with their explicit answer) has
resolved it. Unanswered questions must stay visible so the next review cycle can
address them.

## After revising

1. Commit:
   ```bash
   git add "$doc_path"
   git commit -m "Address design feedback (PR #$pr_number)"
   ```

2. Push:
   ```bash
   branch=$(cloche get current_branch)
   git push origin "$branch"
   ```

3. Update the addressed-at timestamp so the poll loop knows new feedback since
   this point:
   ```bash
   cloche set last_addressed_at "$(date +%s)"
   ```

4. Output:
   ```
   CLOCHE_RESULT:success
   ```

## If you cannot address the feedback

If a comment requires information genuinely unavailable in this repo (e.g., it
references an external decision or system outside your reach), output:
```
CLOCHE_RESULT:give-up
```
with a one-sentence explanation of what is missing.
