# cloche extract

New command `cloche extract <container>` which:
- creates a git branch + worktree 
- wipes it, then copies the contents of the named container's /workflow/ directory into the branch

This takes the place of the current User-side workflow scripts that perform this same task in most dev projects.
