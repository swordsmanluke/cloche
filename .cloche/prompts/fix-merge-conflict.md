# Fix Merge Conflicts

The automated merge step failed because rebasing the agent's feature branch onto the base branch produced conflicts. Resolve the conflicts and complete the merge.

## Setup

Run the following to find the branch to merge:

```bash
cloche get child_run_id
```

This gives you `<run-id>` (e.g. `gwgt-develop`). The feature branch is `cloche/<run-id>`.

The project root is `$CLOCHE_PROJECT_DIR`.

## Process

1. **Identify conflicts** — check out the branch in a worktree and start the rebase:

   ```bash
   git -C "$CLOCHE_PROJECT_DIR" worktree add "$CLOCHE_PROJECT_DIR/.gitworktrees/merge/<run-id>" "cloche/<run-id>"
   git -C "$CLOCHE_PROJECT_DIR/.gitworktrees/merge/<run-id>" rebase "$(git -C "$CLOCHE_PROJECT_DIR" rev-parse --abbrev-ref HEAD)"
   ```

   If the rebase conflicts, git will pause and list the conflicting files.

2. **Resolve each conflict** — for each conflicted file:
   - Read the conflict markers (`<<<<<<<`, `=======`, `>>>>>>>`)
   - **Strategy**: the feature branch's changes are the work to be preserved; main's changes are the context to integrate into. Produce a result that correctly incorporates both. Do not simply pick one side — understand both changes and merge them semantically.
   - For add/add conflicts on files with identical content, keep one copy (`git checkout --ours` or `--theirs` as appropriate, then `git add`).
   - After editing: `git -C <worktree> add <file>`

3. **Continue the rebase** after all conflicts are resolved:

   ```bash
   GIT_AUTHOR_NAME=cloche GIT_AUTHOR_EMAIL=cloche@local \
   GIT_COMMITTER_NAME=cloche GIT_COMMITTER_EMAIL=cloche@local \
   git -C "$CLOCHE_PROJECT_DIR/.gitworktrees/merge/<run-id>" rebase --continue
   ```

4. **Verify** — run the tests from the project root to confirm the merge is correct:

   ```bash
   go test ./...
   ```

   If tests fail, fix the code before proceeding.

5. **Fast-forward main** — once the rebase succeeds:

   ```bash
   REBASED=$(git -C "$CLOCHE_PROJECT_DIR/.gitworktrees/merge/<run-id>" rev-parse HEAD)
   git -C "$CLOCHE_PROJECT_DIR" worktree remove --force "$CLOCHE_PROJECT_DIR/.gitworktrees/merge/<run-id>"
   git -C "$CLOCHE_PROJECT_DIR" update-ref "refs/heads/cloche/<run-id>" "$REBASED"
   git -C "$CLOCHE_PROJECT_DIR" merge --ff-only "cloche/<run-id>"
   git -C "$CLOCHE_PROJECT_DIR" branch -D "cloche/<run-id>"
   ```

## Results

- Report `CLOCHE_RESULT:success` after the fast-forward merge completes.
- Report `CLOCHE_RESULT:fail` if conflicts cannot be resolved correctly or tests still fail after resolution attempts.
