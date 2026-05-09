# Vertical workflow: address PR feedback

The user has left review feedback on the current layer's PR. The pull-comments step
has collected the open comments into a markdown file at `$(clo get feedback_path)`.

Read that file and the diff of the layer's branch (`git log -p main..HEAD` or against
the actual base — see `clo get current_base_branch`). Address every actionable
comment:

- Code change requested → make the change, commit it.
- Question with a clear answer in the diff → reply via `gh api` (a regular comment
  is fine; you don't need to resolve threads, the workflow handles that).
- Suggestion you disagree with → reply with your reasoning. Do not silently ignore.
- Comment that's actually about a deeper layer → reply explaining that this is out
  of scope for this layer and (if appropriate) create or update a downstream layer
  task with `bd`.

## Boundaries

You are inside one layer. The same rules from `vertical-implement.md` apply:

- Don't implement layers below yours just because a comment asked about them.
- Don't refactor unrelated code.
- Don't expand mocks into real implementations — that's the next layer's job.

If the user's feedback fundamentally rejects the layer's design ("we should not do
this in the UI at all, push it to the API"), don't try to invent a new layer plan
inline. Reply on the PR explaining you'll surface this to the workflow, then write
`$(clo get temp_file_dir)/agent-give-up-reason.md` with the situation and exit
non-zero. The workflow will reopen the PR as stuck so the user can give explicit
direction.

## Output

Commit your fixes. The workflow's verify and push steps run after you exit. Once
pushed, the workflow re-enters the poll loop and waits for the user's next response.
