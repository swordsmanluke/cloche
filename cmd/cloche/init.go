package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var workflowTemplate = `workflow "%s" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file(".cloche/prompts/fix.md")
    max_attempts = 2
    results = [success, fail, give-up]
  }

  step update-docs {
    prompt = file(".cloche/prompts/update-docs.md")
    results = [success, fail]
  }

  implement:success -> test
  implement:fail -> abort
  test:success -> update-docs
  test:fail -> fix
  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
  update-docs:success -> done
  update-docs:fail -> done
}
`

var dockerfileTemplate = `FROM %s
USER root

# Add your project's dependencies here.
# Example:
#   RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
#   RUN npm install -g @anthropic-ai/claude-code

USER agent
`

var implementPrompt = `Implement the following change in this project.

## User Request
(Contents of .cloche/prompt.txt will be injected here by the adapter)

## Guidelines
- Follow existing project conventions if files already exist
- Write tests for new functionality
- Run tests locally before declaring success
`

var fixPrompt = `Fix the code based on the validation failures below.
Only modify files that need fixing. Do not rewrite the entire project.

## Validation Output
(Contents of .cloche/output/*.log will be injected here by the adapter)
`

var defaultConfigTOML = `# Cloche project configuration
# Set active = true so cloched auto-runs the main workflow on startup.
active = false

[evolution]
enabled            = true
debounce_seconds   = 30
min_confidence     = "medium"
max_prompt_bullets = 50
`

var updateDocsPrompt = `Review the CLI source code and update usage documentation to reflect any changes.

## What to check
1. Read cmd/cloche/main.go and cmd/cloche/init.go to understand the current CLI surface
2. Compare against docs/USAGE.md

## Sections to keep in sync
- CLI Reference: subcommands, flags, usage examples
- Setting Up a New Project: scaffolding steps, workflow template
- Daemon Configuration: environment variables
- Build Commands: Makefile targets

## Rules
- Only modify docs/USAGE.md (and docs/workflows.md if workflow DSL syntax changed)
- Only make changes when there are actual discrepancies — do not rewrite for style
- If everything is already accurate, make no changes and report success
`

var defaultClocheignore = `# Files excluded from the container workspace.
# Uses gitignore-style patterns (*, ?, **).

# Cloche runtime state
.cloche/*-*-*/
.cloche/run-*/
.cloche/attempt_count/

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

var hostWorkflowTemplate = `# host.cloche — orchestration workflow (runs on host, not in container)
# Steps here execute as the daemon user. Keep operations simple and safe.

workflow "main" {
  step prepare-prompt {
    run     = "bash .cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}
`

var preparePromptScript = `#!/usr/bin/env bash
# Default prompt generator.
# Writes the task prompt to stdout and to $CLOCHE_STEP_OUTPUT.
set -euo pipefail

prompt="## Task: ${CLOCHE_TASK_TITLE}

${CLOCHE_TASK_BODY}"

echo "$prompt"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$prompt" > "$CLOCHE_STEP_OUTPUT"
`

func cmdInit(args []string) {
	workflow := "develop"
	baseImage := "cloche-base:latest"

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
		{filepath.Join(clocheDir, "config.toml"), defaultConfigTOML, 0644},
		{filepath.Join(clocheDir, "prompts", "implement.md"), implementPrompt, 0644},
		{filepath.Join(clocheDir, "prompts", "fix.md"), fixPrompt, 0644},
		{filepath.Join(clocheDir, "prompts", "update-docs.md"), updateDocsPrompt, 0644},
		{filepath.Join(clocheDir, "version"), versionContent, 0644},
		{filepath.Join(clocheDir, "host.cloche"), hostWorkflowTemplate, 0644},
		{filepath.Join(clocheDir, "scripts", "prepare-prompt.sh"), preparePromptScript, 0755},
		{".clocheignore", defaultClocheignore, 0644},
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

	removeGitignoreEntries([]string{
		".cloche/*/",
		"!.cloche/prompts/",
		"!.cloche/overrides/",
		"!.cloche/evolution/",
	})

	addGitignoreEntries([]string{
		".cloche/*-*-*/",
		".cloche/run-*/",
		".cloche/attempt_count/",
		".gitworktrees/",
	})

	// Generate shell completion scripts into ~/.cloche/completions/.
	home := os.Getenv("HOME")
	if home != "" {
		completionsDir := filepath.Join(home, ".cloche", "completions")
		generateCompletionScripts(completionsDir)
	}

	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "\nInitialized Cloche project in %s\n", filepath.Base(cwd))
	fmt.Fprintf(os.Stderr, "\nNext steps:\n")
	fmt.Fprintf(os.Stderr, "  1. Edit %s    — set active = true to auto-run on startup\n", filepath.Join(clocheDir, "config.toml"))
	fmt.Fprintf(os.Stderr, "  2. Edit %s — adjust the test command for your project\n", workflowFile)
	fmt.Fprintf(os.Stderr, "  3. Edit %s     — add your project's dependencies\n", filepath.Join(clocheDir, "Dockerfile"))
	fmt.Fprintf(os.Stderr, "  4. Edit %s — customize prompt generation\n", filepath.Join(clocheDir, "scripts", "prepare-prompt.sh"))
	fmt.Fprintf(os.Stderr, "  5. Add container-specific overrides to %s/ (e.g. CLAUDE.md)\n", filepath.Join(clocheDir, "overrides"))
	fmt.Fprintf(os.Stderr, "  6. docker build -t cloche-agent -f %s .\n", filepath.Join(clocheDir, "Dockerfile"))
	fmt.Fprintf(os.Stderr, "  7. cloche run --workflow %s --prompt \"...\"\n", workflow)
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