# Cloche: Web UI 

The Cloche Daemon provides a clean web interface for managing Cloche Projects and following the current status of the tasks being performed.

## Landing page: /
The root landing page, displays the current status of the Cloche system and lists all recent / ongoing workflow runs.

[mockup]

## Projects Page: /projects

The default page in the UI, the Projects page displays a card for each Project known to the Daemon.

The Card contains:
- Status icon (red, green, yellow) indicating health of the recent task runs
- Name
- recent/passed ratio
- Active runner count
- Colored Dots for the past 10 run results indicating their success/failure status
- Action buttons: View, Start/Stop Orchestrator
  - View: takes the user to the Project Details page
  - Start/Stop Orchestrator: [Start|Stop] text is determined by whether the Orchestration loop for the project is currently running or not. Clicking the button will disable/enable the Orchestration loop. Changing the loop state only impacts picking up new Tasks and will not e.g. abort work in progress.

[mockup]

## Project Details Page: /project/<project slug>/
This displays information about the project:
- Tasks (only pending and in-progress)
- Configured Repositories
- Configured Agents
- Workflows

[mockup]

## Project Runs Page: /project/<project slug>/runs
Runs page shows the Workflow runs, organized by Task and sorted by time (newest at the top). 

The Run list item contains:
- Task ID & Title
- Status (pending, running, success, failed)
- Attempt List
- The Attempt list shows each Attempt at performing the Task.
- Attempt #; Status; Start Time
- Workflow Step list
- Run ID link (goes to the Run Details page)
- Workflow; Location (host/container); State; Step; Duration & ‘start time ago’; Container ID; Error message

[mockup]

## Run Details Page: /project/<project slug>/run/<run id>
Displays the steps of a workflow (live or completed) and each step's associated log output.

[mockup]


