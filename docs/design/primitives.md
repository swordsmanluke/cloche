# Cloche: Primitives

## Workflow
The Workflow is the core, functional primitive in Cloche. A Workflow contains Steps and Wires which define the logical flow through the system. 

The .cloche files within the Project’s configuration define the Steps to be executed, the Wires each Step may emit and the connections between them.

## Step
A Step represents an action being taken - it could be a Command, a Prompt, or even Poll while awaiting instructions. 

- A Command step executes the provided shell command (usually a script though any shell command is valid).

- A Prompt step passes the contents of a provided prompt template file to the Agent. (modulo template variable replacement).

- A Poll step executes a provided command until it fails or indicates the next Wire in the workflow to follow.

- A Sub-Workflow can be initiated as well - the calling Workflow will pause and await the sub-Workflow’s completion before continuing. 

### Wire
A Wire represents an event or transition action upon exiting a Step. By convention any Step will follow the success Wire if its internal script/prompt exited with a zero-exit-code and failure if the internal script/prompt exited with a non-zero-exit-code. 

Individual Steps may send execution down other Wires by explicitly indicating to Cloche which Wire to follow.  

## Agent
An Agent is an executable local Harness + remote LLM system such as Claude Code, Codex, OpenCode etc. The Harness accepts user prompts, processes them with the LLM and takes some self-directed actions (such as reading/editing files, running shell processes, etc) as indicated by the LLM’s tool-calling needs, before displaying output results to the User.

Cloche builds containerized environments for the User’s Agent(s) of choice and manages an automated Workflow to ensure the User’s requested Task is performed correctly.

## Task
A unit of work to be performed by a Project’s Workflow. A Workflow receiving a Task uses the data within to drive actions. (A prototypical Task would be a description of a code change to make, though it could also be a non-development-related Task, such as updating documentation, researching a topic, downloading materials, etc.) 

Cloche maintains a cache of Tasks retrieved from the User’s preferred work-tracking system (e.g. Jira, Beads, Github Issues, ADO, etc) and assigns them to Workflow runners in accordance with the Project’s configured concurrency limits as well as the Task’s defined dependencies. 

## Project
A Project is a named collection of Workflows, Tasks, Agents and Repositories which represents a logical unit the User wishes to combine. For instance, a basic videogame Project could have some Workflows for developing software features, managing (or even generating!) assets; Agents defined for writing code, writing dialogue and generating graphics; and separate Repositories for the source code and image/sound assets.

## Repository
A Repository is a directory and sync mechanism for storing files. Currently, the only supported sync software is git. A Project has at least one configured Repository, but can have as many as it needs. A SAAS Product could have separate frontend and a backend Repositories, a videogame could separate its source code from its asset files, and so forth.

## Container
A Container is an enclosed environment for running Agents within, to avoid granting it access to the full host environment. A Container may be configured with limited access to host resources (such as only allow-listed network access) and files. 


