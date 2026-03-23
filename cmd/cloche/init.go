package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/config"
)

var workflowTemplate = `workflow "%s" {
  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step commit {
    run     = "git add -A && git commit -m \"implement task changes\" || true"
    results = [success, fail]
  }

  step test {
    run     = "echo 'TODO(cloche-init): replace with your test command'"
    results = [success, fail]
  }

  step fix-tests {
    prompt       = file(".cloche/prompts/fix-tests.md")
    max_attempts = 2
    results      = [success, fail]
  }

  implement:success  -> commit
  implement:fail     -> abort
  commit:success     -> test
  commit:fail        -> abort
  test:success       -> done
  test:fail          -> fix-tests
  fix-tests:success  -> test
  fix-tests:fail     -> abort
}
`

var dockerfileTemplate = `FROM %s
USER root

# TODO(cloche-init): install your project's runtime and build dependencies.
# Uncomment and adapt one of the examples below, or write your own.
#
# Python:
#   RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-pip \
#    && rm -rf /var/lib/apt/lists/*
#
# Node.js (LTS via NodeSource):
#   RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
#    && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*
#
# Go (use the official golang image as base instead):
#   FROM golang:1.22 AS base
#
# Java:
#   RUN apt-get update && apt-get install -y --no-install-recommends default-jdk \
#    && rm -rf /var/lib/apt/lists/*
#
# Ruby:
#   RUN apt-get update && apt-get install -y --no-install-recommends ruby ruby-dev \
#    && rm -rf /var/lib/apt/lists/*

USER agent
`

var implementPrompt = `Implement the following change in this project.

## Task

{task_description}

## Project Context

TODO(cloche-init): describe your project here so the agent has the context it needs. Examples:
- Language: Go — run tests with "go test ./..."
- Language: Node.js/TypeScript — run tests with "npm test"
- Language: Python — run tests with "pytest"
- Key constraints: follow existing patterns, don't modify generated files

## Guidelines
- Follow existing project conventions
- Write tests for new functionality
- Run tests locally before declaring success
`

var fixTestsPrompt = `The tests are failing. Fix the code so all tests pass.

Do not modify the test files — fix the implementation instead.

## Test Output

{previous_output}
`

var defaultConfigTOMLTemplate = `# Cloche project configuration
# Set active = true so cloched picks up tasks automatically.
active = false

[daemon]
image = "%s"

# [orchestration]
# concurrency              = 1
# stagger_seconds          = 1.0
# dedup_seconds            = 300.0
# stop_on_error            = false
# max_consecutive_failures = 3

[evolution]
enabled            = true
debounce_seconds   = 30
min_confidence     = "medium"
max_prompt_bullets = 50
`

var defaultClocheignore = `# Files excluded from the container workspace.
# Uses gitignore-style patterns (*, ?, **).

# Cloche runtime state
.cloche/logs/
.cloche/runs/

# Common large/generated directories
node_modules/
.venv/
venv/
__pycache__/
dist/
build/
.next/
target/

# IDE / editor
.idea/
.vscode/
*.swp
*.swo
`

var versionContent = "1\n"

var hostWorkflowTemplate = `workflow "list-tasks" {
  host {}

  step get-tasks {
    run     = "python3 .cloche/scripts/get-tasks.py"
    results = [success, fail]
  }

  get-tasks:success -> done
  get-tasks:fail    -> abort
}

workflow "main" {
  host {}

  step claim-task {
    run     = "python3 .cloche/scripts/claim-task.py"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  claim-task:success -> develop
  claim-task:fail    -> abort
  develop:success    -> done
  develop:fail       -> done
}

workflow "finalize" {
  host {}

  step prepare-merge {
    run     = "python3 .cloche/scripts/prepare-merge.py"
    results = [success, fail]
  }

  step fix-merge {
    prompt  = file(".cloche/prompts/fix-merge.md")
    results = [success, fail]
  }

  step merge {
    run     = "python3 .cloche/scripts/merge.py"
    results = [success, fail]
  }

  step release-task {
    run     = "python3 .cloche/scripts/release-task.py"
    results = [success, fail]
  }

  step cleanup {
    run     = "python3 .cloche/scripts/cleanup.py"
    results = [success, fail]
  }

  step unclaim {
    run     = "python3 .cloche/scripts/unclaim.py"
    results = [success, fail]
  }

  prepare-merge:success -> merge
  prepare-merge:fail    -> fix-merge
  fix-merge:success     -> merge
  fix-merge:fail        -> unclaim
  merge:success         -> release-task
  merge:fail            -> fix-merge
  release-task:success  -> cleanup
  release-task:fail     -> unclaim
  cleanup:success       -> done
  cleanup:fail          -> unclaim
  unclaim:success       -> abort
  unclaim:fail          -> abort
}
`

var getTasksPyScript = `#!/usr/bin/env python3
"""Read the next open task from .cloche/task_list.json.

Replace this script with one that reads from your task tracker of choice
(Linear, Jira, GitHub Issues, etc.) and returns ready tasks as JSONL:

    {"id": "...", "title": "...", "description": "...", "status": "open"}

One JSON object per line. The orchestration loop picks the first open task.
"""
import json
import os
import sys

task_file = os.path.join(os.environ.get("CLOCHE_PROJECT_DIR", "."), ".cloche", "task_list.json")

with open(task_file) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        task = json.loads(line)
        if task.get("status") == "open":
            output = json.dumps(task)
            print(output)
            output_path = os.environ.get("CLOCHE_STEP_OUTPUT")
            if output_path:
                with open(output_path, "w") as out:
                    out.write(output + "\n")
            sys.exit(0)

# No open tasks — exit success with empty output (loop idles)
output_path = os.environ.get("CLOCHE_STEP_OUTPUT")
if output_path:
    with open(output_path, "w") as out:
        out.write("")
`

var claimTaskPyScript = `#!/usr/bin/env python3
"""Mark the assigned task as in-progress and pass its description downstream.

Prints the task description to stdout so the next workflow step (develop)
receives it as the prompt for the coding agent.
"""
import json
import os
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")
task_file = os.path.join(project_dir, ".cloche", "task_list.json")

if not task_id:
    print("error: CLOCHE_TASK_ID not set", file=sys.stderr)
    sys.exit(1)

tasks = []
with open(task_file) as f:
    for line in f:
        line = line.strip()
        if line:
            tasks.append(json.loads(line))

target = None
for task in tasks:
    if task["id"] == task_id:
        task["status"] = "in-progress"
        target = task
        break

if target is None:
    print(f"error: task {task_id} not found", file=sys.stderr)
    sys.exit(1)

with open(task_file, "w") as f:
    for task in tasks:
        f.write(json.dumps(task) + "\n")

# Print description to stdout — the host executor captures it as step output,
# which the develop step reads as its prompt.
print(target.get("description", target.get("title", "")))
`

var prepareMergePyScript = `#!/usr/bin/env python3
"""Create a worktree and rebase the agent's branch onto the base branch."""
import os
import subprocess
import sys

project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")

# The branch is named after the child container run ID, not the host main run ID.
run_id = subprocess.run(
    ["cloche", "get", "child_run_id"], check=True, capture_output=True, text=True,
).stdout.strip()

if not run_id:
    print("error: child_run_id not set in run context", file=sys.stderr)
    sys.exit(1)

branch = f"cloche/{run_id}"
worktree_dir = os.path.join(project_dir, ".gitworktrees", "merge", run_id)

# Verify branch exists
try:
    subprocess.run(
        ["git", "-C", project_dir, "rev-parse", "--verify", branch],
        check=True, capture_output=True,
    )
except subprocess.CalledProcessError:
    print(f"error: branch {branch} does not exist", file=sys.stderr)
    sys.exit(1)

base_branch = subprocess.run(
    ["git", "-C", project_dir, "rev-parse", "--abbrev-ref", "HEAD"],
    check=True, capture_output=True, text=True,
).stdout.strip()

os.makedirs(os.path.dirname(worktree_dir), exist_ok=True)

env = {**os.environ,
       "GIT_AUTHOR_NAME": "cloche", "GIT_AUTHOR_EMAIL": "cloche@local",
       "GIT_COMMITTER_NAME": "cloche", "GIT_COMMITTER_EMAIL": "cloche@local"}

subprocess.run(
    ["git", "-C", project_dir, "worktree", "add", worktree_dir, branch],
    check=True, env=env,
)

# Store worktree path for fix-merge / merge steps
subprocess.run(["cloche", "set", "worktree_path", worktree_dir], check=True)
subprocess.run(["cloche", "set", "base_branch", base_branch], check=True)

# Rebase onto base branch
result = subprocess.run(
    ["git", "-C", worktree_dir, "rebase", base_branch],
    env=env,
)
if result.returncode != 0:
    subprocess.run(["git", "-C", worktree_dir, "rebase", "--abort"], capture_output=True)
    print(f"error: rebase failed — worktree preserved at {worktree_dir}", file=sys.stderr)
    sys.exit(1)

print(f"Rebased {branch} onto {base_branch}")
`

var mergePyScript = `#!/usr/bin/env python3
"""Fast-forward base branch to the rebased agent branch."""
import os
import subprocess
import sys

worktree_dir = subprocess.run(
    ["cloche", "get", "worktree_path"], check=True, capture_output=True, text=True,
).stdout.strip()

project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")

# The branch is named after the child container run ID.
run_id = subprocess.run(
    ["cloche", "get", "child_run_id"], check=True, capture_output=True, text=True,
).stdout.strip()
branch = f"cloche/{run_id}"

env = {**os.environ,
       "GIT_AUTHOR_NAME": "cloche", "GIT_AUTHOR_EMAIL": "cloche@local",
       "GIT_COMMITTER_NAME": "cloche", "GIT_COMMITTER_EMAIL": "cloche@local"}

# Get rebased HEAD
rebased_head = subprocess.run(
    ["git", "-C", worktree_dir, "rev-parse", "HEAD"],
    check=True, capture_output=True, text=True,
).stdout.strip()

# Remove worktree before merging
subprocess.run(
    ["git", "-C", project_dir, "worktree", "remove", "--force", worktree_dir],
    capture_output=True,
)

# Update branch ref and fast-forward
subprocess.run(
    ["git", "-C", project_dir, "update-ref", f"refs/heads/{branch}", rebased_head],
    check=True, env=env,
)
subprocess.run(
    ["git", "-C", project_dir, "merge", "--ff-only", branch],
    check=True, env=env,
)

# Delete the feature branch
subprocess.run(
    ["git", "-C", project_dir, "branch", "-D", branch],
    capture_output=True,
)

print(f"Merged {branch} ({rebased_head[:8]})")
`

var releaseTaskPyScript = `#!/usr/bin/env python3
"""Mark the completed task as done and move it to the end of .cloche/task_list.json."""
import json
import os
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")
task_file = os.path.join(project_dir, ".cloche", "task_list.json")

if not task_id:
    print("error: CLOCHE_TASK_ID not set", file=sys.stderr)
    sys.exit(1)

tasks = []
with open(task_file) as f:
    for line in f:
        line = line.strip()
        if line:
            tasks.append(json.loads(line))

target = None
remaining = []
for task in tasks:
    if task["id"] == task_id:
        task["status"] = "done"
        target = task
    else:
        remaining.append(task)

if target is None:
    print(f"error: task {task_id} not found", file=sys.stderr)
    sys.exit(1)

remaining.append(target)

with open(task_file, "w") as f:
    for task in remaining:
        f.write(json.dumps(task) + "\n")

print(f"Released task {task_id}")
`

var cleanupPyScript = `#!/usr/bin/env python3
"""Clean up the worktree and branch from the develop run."""
import os
import subprocess

project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")

# The branch is named after the child container run ID.
result = subprocess.run(
    ["cloche", "get", "child_run_id"], capture_output=True, text=True,
)
run_id = result.stdout.strip() if result.returncode == 0 else ""

if not run_id:
    print("warning: child_run_id not set, skipping cleanup")
else:
    branch = f"cloche/{run_id}"
    worktree_dir = os.path.join(project_dir, ".gitworktrees", "cloche", run_id)

    if os.path.isdir(worktree_dir):
        subprocess.run(
            ["git", "-C", project_dir, "worktree", "remove", "--force", worktree_dir],
            capture_output=True,
        )

    subprocess.run(
        ["git", "-C", project_dir, "worktree", "prune"],
        capture_output=True,
    )

    subprocess.run(
        ["git", "-C", project_dir, "branch", "-D", branch],
        capture_output=True,
    )

print(f"Cleaned up run {run_id or 'unknown'}")
`

var unclaimPyScript = `#!/usr/bin/env python3
"""Reset the task to open and stop the orchestration loop.

This is the emergency brake — it halts all automated work so a human
can investigate what went wrong.
"""
import json
import os
import subprocess
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")
task_file = os.path.join(project_dir, ".cloche", "task_list.json")

if task_id:
    tasks = []
    with open(task_file) as f:
        for line in f:
            line = line.strip()
            if line:
                tasks.append(json.loads(line))

    for task in tasks:
        if task["id"] == task_id:
            task["status"] = "open"
            break

    with open(task_file, "w") as f:
        for task in tasks:
            f.write(json.dumps(task) + "\n")

    print(f"Unclaimed task {task_id}")

# Stop the loop — human must investigate and resume
subprocess.run(["cloche", "loop", "stop"])
print("Loop stopped — investigate and run 'cloche loop resume' when ready")
`

var fixMergePrompt = `A rebase of the agent's branch onto the base branch has failed due to conflicts.

The conflicting worktree is at the path stored in the ` + "`worktree_path`" + ` context key
(retrieve with ` + "`cloche get worktree_path`" + `).

Resolve the conflicts in the worktree, then run:
  git -C <worktree_path> rebase --continue

Do not abort the rebase. If you cannot resolve the conflicts, report failure.
`

var taskListJSON = `{"id": "1", "title": "Validate Agent works", "description": "Create a file, ./agent_test containing the string 'I exist!'", "status": "open"}
{"id": "2", "title": "Clean up cloche test files", "description": "Delete ./agent_test and cloche_init_test/cloche/test_cloche.py - they were created for validation and we're done now", "status": "open"}
`

var testClocheScript = `#!/usr/bin/env python3
"""Cloche environment validation tests.

These tests verify that the agent successfully completed the setup task.
Delete this file once validation is complete (task #2 does this automatically).
"""
import os
import unittest

PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

class TestAgentSetup(unittest.TestCase):
    def test_agent_test_file_exists(self):
        path = os.path.join(PROJECT_ROOT, "agent_test")
        self.assertTrue(os.path.isfile(path), "agent_test file does not exist")

    def test_agent_test_file_contents(self):
        path = os.path.join(PROJECT_ROOT, "agent_test")
        with open(path) as f:
            contents = f.read().strip()
        self.assertEqual(contents, "I exist!")

if __name__ == "__main__":
    unittest.main()
`

func projectImageName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "cloche-agent:latest"
	}
	return strings.ToLower(filepath.Base(cwd)) + "-cloche-agent:latest"
}

func cmdInit(args []string) {
	workflow := "develop"
	baseImage := "cloche-agent:latest"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflow = args[i]
			}
		case "--base-image":
			if i+1 < len(args) {
				i++
				baseImage = args[i]
			}
		}
	}

	imageName := projectImageName()

	clocheDir := ".cloche"
	workflowFile := filepath.Join(clocheDir, workflow+".cloche")

	// Refuse to overwrite existing workflow
	if _, err := os.Stat(workflowFile); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists\n", workflowFile)
		os.Exit(1)
	}

	// Create directories
	for _, dir := range []string{
		clocheDir,
		filepath.Join(clocheDir, "prompts"),
		filepath.Join(clocheDir, "overrides"),
		filepath.Join(clocheDir, "scripts"),
		filepath.Join("cloche_init_test", "cloche"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating %s/: %v\n", dir, err)
			os.Exit(1)
		}
	}

	// Write all files, skipping any that already exist
	files := []struct {
		path    string
		content string
		mode    os.FileMode
	}{
		{workflowFile, fmt.Sprintf(workflowTemplate, workflow), 0644},
		{filepath.Join(clocheDir, "Dockerfile"), fmt.Sprintf(dockerfileTemplate, baseImage), 0644},
		{filepath.Join(clocheDir, "config.toml"), fmt.Sprintf(defaultConfigTOMLTemplate, imageName), 0644},
		{filepath.Join(clocheDir, "prompts", "implement.md"), implementPrompt, 0644},
		{filepath.Join(clocheDir, "prompts", "fix-tests.md"), fixTestsPrompt, 0644},
		{filepath.Join(clocheDir, "prompts", "fix-merge.md"), fixMergePrompt, 0644},
		{filepath.Join(clocheDir, "version"), versionContent, 0644},
		{filepath.Join(clocheDir, "host.cloche"), hostWorkflowTemplate, 0644},
		{filepath.Join(clocheDir, "scripts", "get-tasks.py"), getTasksPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "claim-task.py"), claimTaskPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "prepare-merge.py"), prepareMergePyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "merge.py"), mergePyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "release-task.py"), releaseTaskPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "cleanup.py"), cleanupPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "unclaim.py"), unclaimPyScript, 0755},
		{".clocheignore", defaultClocheignore, 0644},
		{filepath.Join(clocheDir, "task_list.json"), taskListJSON, 0644},
		{filepath.Join("cloche_init_test", "cloche", "test_cloche.py"), testClocheScript, 0644},
	}

	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			fmt.Fprintf(os.Stderr, "  skip %s (already exists)\n", f.path)
			continue
		}
		if err := os.WriteFile(f.path, []byte(f.content), f.mode); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", f.path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "  create %s\n", f.path)
	}

	addGitignoreEntries([]string{
		".cloche/logs/",
		".cloche/runs/",
		".cloche/output/",
		".cloche/history.log",
		".gitworktrees/",
		".cloche/task_list.json",
	})

	// Create global daemon config if it doesn't exist.
	if cfgPath, err := config.WriteGlobalConfigIfAbsent(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write global config: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "  create %s\n", cfgPath)
	}

	// Generate shell completion scripts into ~/.cloche/completions/.
	home := os.Getenv("HOME")
	if home != "" {
		completionsDir := filepath.Join(home, ".cloche", "completions")
		generateCompletionScripts(completionsDir)
	}

	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "\nInitialized Cloche project in %s\n", filepath.Base(cwd))
	fmt.Fprintf(os.Stderr, "\nNext steps:\n")
	fmt.Fprintf(os.Stderr, "  1. Edit .cloche/config.toml           — set active = true\n")
	fmt.Fprintf(os.Stderr, "  2. Edit %s        — adjust the test command for your project\n", workflowFile)
	fmt.Fprintf(os.Stderr, "  3. Edit .cloche/Dockerfile            — add your project's dependencies\n")
	fmt.Fprintf(os.Stderr, "  4. Edit .cloche/scripts/get-tasks.py  — connect to your task tracker\n")
	fmt.Fprintf(os.Stderr, "  5. docker build -t %s -f .cloche/Dockerfile .\n", imageName)
	fmt.Fprintf(os.Stderr, "  6. cloche loop                        — start the orchestration loop\n")
	fmt.Fprintf(os.Stderr, "\nThe sample tasks in .cloche/task_list.json verify your setup end-to-end.\n")
	fmt.Fprintf(os.Stderr, "Task #1 asks the agent to create a file; task #2 cleans up after itself.\n")
}

func removeGitignoreEntries(entries []string) {
	const gitignore = ".gitignore"

	existing, err := os.ReadFile(gitignore)
	if err != nil {
		return
	}

	lines := strings.Split(string(existing), "\n")
	removeSet := make(map[string]bool, len(entries))
	for _, e := range entries {
		removeSet[e] = true
	}

	var filtered []string
	for _, line := range lines {
		if !removeSet[strings.TrimSpace(line)] {
			filtered = append(filtered, line)
		}
	}

	result := strings.Join(filtered, "\n")
	os.WriteFile(gitignore, []byte(result), 0644)
}

func addGitignoreEntries(entries []string) {
	const gitignore = ".gitignore"

	existing, _ := os.ReadFile(gitignore)
	content := string(existing)

	var toAdd []string
	for _, entry := range entries {
		if !strings.Contains(content, entry) {
			toAdd = append(toAdd, entry)
		}
	}
	if len(toAdd) == 0 {
		return
	}

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(toAdd, "\n") + "\n"

	if err := os.WriteFile(gitignore, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}
}
