# Cloche feedback

I had a friend of mine try out Cloche and he wrote back some very useful feedback.

Some of the feedback still requires some design. Others should be converted directly
into bead tasks for implementation. 

Those items that are ready to be implemented, I've marked with a priority value, e.g. P1, P2, etc
The tickets should be prioritized accordingly.

Feedback items lacking a priority I'm still thinking about and we should create design proposal
documents in ./docs/ where I can break down my thoughts and come up with an approach.

Design Proposal documents should include
1. the original feedback
2. my attached notes (if any)

I'll take it from there - you don't need to add any proposal of your own to the doc, just extract
the useful information and put it in a new file for me.

Thus, when you read this document, each item here has an action for you to take.
Either:
- create a bead task to implement the item
- create a design proposal document for me to continue working in.


## WebUI

### Project Runs page
-   P3: Add an indicator for Cloche loop enabled or not
-   P1: Add a button to start/stop the cloche loop
-   Add a button to stop a run. (This should also unclaim the task I think? How do we prevent tasks from being in a bad state and getting immediately picked up again? Would it just kill cloche loop?)
-   P3: Color code steps by Host / Container (and add a single line border for Host and double-line for Container steps, for color-blind support)

### Log Presentation

-   P3: Interpret or remove ANSI color codes (\[90m, etc)so that I can see the colors. Or at least remove them so the text is readable.
-   P1: The 'agent' column is always empty - but it should display> 'claude' or 'codex' etc - whichever agent is used in the Step.
-   P1: An error instructs: \"Loop stopped --- investigate and run \'cloche loop resume\' when ready\" But cloche loop resume isn't a real command. It's either cloche loop or cloche resume \<task\> 

## Core

-   As a \"large\" organization, I have several projects that all follow
    > the exact same workflow, but with some minor changes that capture
    > project specific configs. Currently I need to copy cloche config
    > files around to each of these projects. What if I discover a bug
    > in one of my cloche workflows? I\'ll have to carefully propagate
    > changes to other projects and make sure I don\'t overwrite project
    > specific configs. Is there a way to separate project-agnostic
    > config from project-specific config? I mean I could do that
    > manually, but would it make sense to have some functionality built
    > into cloche specifically for that? It\'d be good to have a clear
    > path for propagating initial configuration to projects and to
    > propagate changes.

-   I use git submodules to share code between projects. Which means if
    > there are changes made to a submodule, that has to be committed
    > and pushed first, then the submodule ref needs to be committed in
    > the parent repository. I have no idea how cloche handles this, and
    > I haven\'t actually run into it yet but I can see it coming in the
    > future.

-   Copying .cloche files from another directory and running cloche init
    > does not work - I get \".cloche/develop.cloche already exists\".
    > This would prevent someone from checking out a copy of a repo
    > that\'s already been setup with cloche and getting their instance
    > of cloche working on it.
    -   The re-initialization flow needs defining
    -   cloche init should only set up bare min, move the bells and
        > whistles to a command switch.

## Unattended development
-   I have cloche integrated with my ADO instance and I\'m pretty much
    > gotten to a place where I can do truly automated development. It
    > will write code, push a PR to ADO where my build pipeline will run
    > and verify the integration, artifacts are pushed to the server so
    > I can test on any configured device. I can merge the PR from ADO
    > so the code is in my devel branch. I can make comments on the PR
    > and tell cloche (through ADO status) that the work item needs
    > revision and it will address the code review comments.

    > For this to work, my workflow scripts need to do some git shenanigans.
    > Since cloche does its own management of git state, I\'m not sure where
    > the line is between what\'s reasonable for my scripts to do vs. what
    > cloche expected the git state to be, or where that line might drift in
    > the future. For example, the container must make sure to have checked
    > out the PR branch, which has created a lot of pain in iterating on the
    > cloche workflows, because I have to do a bunch of manual git
    > shenanigans to make sure the system is in the right state for me to
    > test. Any time there\'s a problem I ask claude to fix it, and it\'s
    > really hard to tell if claude\'s fix (a) actually fixed the problem
    > and (b) made the system more robust rather than more fragile. The only
    > way to test is to go through the entire revision cycle and see what
    > happens. It\'d be nice if there was a way to run a \"cloche unit
    > test\" or something like that.
  -   I want to dig into this more. Most of cloche's git branch management
        > happens in the scripts. Mostly, I think cloche ignores git, so it
        > should be okay, but need to check.

-  Iteration cycles with cloche are costly. Every time I
    > update PR comments and ask it to address them, it spins up a new
    > container with a fresh context so the LLM must spend a bunch of
    > tokens rereading files that it read in the last iteration of the
    > PR. It\'d be nice if there was a way to mitigate this cost by
    > preserving some context from the previous work on that same PR.
    -   Cloche console?
    -   What about multi-stage workflows that preserve the container(s)
        > until the task is closed
        -   No cleanup step, just named containers, to ensure they stick
            > around
        -   When main "completes", don't close the task, leave it in
            > 'review' or whatever
        -   a branch in 'list-tasks' that checks review comments, then
            > kicks off a workflow for addressing them *inside* the
            > previously existing container?
        -   What about a 'loop' type workflow which defines a script and
            > a downstream workflow? Then the 'loop' workflows are
            > registered with the project. The main loop becomes 'look
            > for task, assign the task to main'. Loops differ in that
            > they don't wait for their sub-workflow to complete. They
            > are a polling primitive that looks for "Work", then
            > assigns it to a workflow.

-   It\'d be nice to have a way to have pluggable connections to task
    > tracking systems. With the right abstraction, a plugin for ado or
    > jira or whatever could be dropped in and new users could be off to
    > the races pretty quickly.

-   I\'ve had to add a number of files to .cloche to communicate back
    > and forth between the agent and cloche on the host. \\\`clo
    > set\\\` is not sufficient for some things, like writing detailed
    > markdown (for PR description, comment replies, etc). This resulted
    > in a lot of churn as claude would forget to add the files to
    > .gitignore and then they\'d end up getting commited in the
    > container. I eventually instructed claude to group all these files
    > into a folder so we could add just the folder to the gitignore.
    > It\'d be good to have a bespoke directory for this in the docs so
    > claude doesn\'t make this mistake in the future.
    -   Set up a standard folder and mechanism for xferring files
        > between steps that isn't git-based.

-   There\'s also a barrier here for truly unattended development that I
    > don\'t know how I should solve. Every time I create a task, cloche
    > will start that task from the state of my working directory. But
    > since I don\'t have to manually touch that directory, it will end
    > up being behind my development branch on the server and cloche
    > will not be able to build on top of features that it built
    > previously unless I manually do a git pull in that directory.
    -   Cloche treats the working directory's checked-out branch as the
        > base.
    -   Could you have a 'git pull' in the main workflow, to keep local
        > up-to-date with what's been merged, before kicking off the
        > 'develop' workflow (which would build the container, etc)

-   I believe cloche would benefit tremendously from having some bespoke
    > features to manage a review/test cycle that involves a human in
    > the loop at certain points. I\'m not sure what that would look
    > like.

