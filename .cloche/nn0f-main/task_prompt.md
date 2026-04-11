## Task: WebUI: Only draw timeout wire when explicitly connected

In the workflow visualizer, the `timeout` wire is currently drawn for every step even when it has not been explicitly wired. Since an unconnected `timeout` wire implicitly goes to `abort` (same as `fail`), drawing it adds visual noise without conveying useful information.

Only render the timeout wire when it has been explicitly connected to a step in the workflow definition.
