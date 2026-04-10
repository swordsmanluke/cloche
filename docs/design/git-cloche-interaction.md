# Design: How Cloche Interacts with Git

## Git Submodules

### Original Feedback

> I use git submodules to share code between projects. Which means if
> there are changes made to a submodule, that has to be committed
> and pushed first, then the submodule ref needs to be committed in
> the parent repository. I have no idea how cloche handles this, and
> I haven't actually run into it yet but I can see it coming in the
> future.

---

## Git State Management in Unattended Development

### Original Feedback

> For this to work, my workflow scripts need to do some git shenanigans.
> Since cloche does its own management of git state, I'm not sure where
> the line is between what's reasonable for my scripts to do vs. what
> cloche expected the git state to be, or where that line might drift in
> the future. For example, the container must make sure to have checked
> out the PR branch, which has created a lot of pain in iterating on the
> cloche workflows, because I have to do a bunch of manual git
> shenanigans to make sure the system is in the right state for me to
> test. Any time there's a problem I ask claude to fix it, and it's
> really hard to tell if claude's fix (a) actually fixed the problem
> and (b) made the system more robust rather than more fragile. The only
> way to test is to go through the entire revision cycle and see what
> happens. It'd be nice if there was a way to run a "cloche unit
> test" or something like that.

### Notes

> I want to dig into this more. Most of cloche's git branch management
> happens in the scripts. Mostly, I think cloche ignores git, so it
> should be okay, but need to check.
