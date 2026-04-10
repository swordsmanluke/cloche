# Design: Working Directory Sync for Unattended Development

## Original Feedback

> There's also a barrier here for truly unattended development that I
> don't know how I should solve. Every time I create a task, cloche
> will start that task from the state of my working directory. But
> since I don't have to manually touch that directory, it will end
> up being behind my development branch on the server and cloche
> will not be able to build on top of features that it built
> previously unless I manually do a git pull in that directory.

### Notes

- Cloche treats the working directory's checked-out branch as the base.
- Could you have a 'git pull' in the main workflow, to keep local
  up-to-date with what's been merged, before kicking off the 'develop'
  workflow (which would build the container, etc)?
