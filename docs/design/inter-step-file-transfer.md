# Design: Inter-Step File Transfer Mechanism

## Original Feedback

> I've had to add a number of files to .cloche to communicate back
> and forth between the agent and cloche on the host. `clo set` is not
> sufficient for some things, like writing detailed markdown (for PR
> description, comment replies, etc). This resulted in a lot of churn
> as claude would forget to add the files to .gitignore and then they'd
> end up getting committed in the container. I eventually instructed
> claude to group all these files into a folder so we could add just
> the folder to the gitignore. It'd be good to have a bespoke directory
> for this in the docs so claude doesn't make this mistake in the future.

### Notes

- Set up a standard folder and mechanism for transferring files between
  steps that isn't git-based.
