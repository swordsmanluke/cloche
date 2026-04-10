# Design: Human-in-the-Loop Review / Test Cycle

## Original Feedback

> I believe cloche would benefit tremendously from having some bespoke
> features to manage a review/test cycle that involves a human in
> the loop at certain points. I'm not sure what that would look like.


# Ideas

What about "human" step type? This would be a special type with a "check status" script that gets polled to see if results are available yet.

Example:
a "Code Review" step with pending/fail/approved/fix statuses. 
- pending: indicates to the cloche that no signal has yet been returned. Keeps this script in the orchestration loop.
- fail: an exit-code != 0 indicating a failure in the script itself. Up to the user config how this gets handled. e.g. fail could go to 'abort' or it could loop back to itself for a retry.
- approved: proceed to whatever the next step is required in the process. a mergequeue workflow, for instance
- fix: this indicates the human requested changes and could go to a 'address-pr-feedback' container-side workflow step which retrieves the PR
feedback and fixes the code accordingly.

`pending` and `fail` would be exit code driven, while (in this scenario) `approved` and `fixit` would come from the usual stdout path.

This allows for polling for human-driven actions without "complete"ing a workflow. 


