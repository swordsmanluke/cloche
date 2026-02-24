package main

import (
	"fmt"
	"os"
	"path/filepath"
)

var workflowTemplate = `workflow "%s" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> fix
  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
`

var dockerfileTemplate = `FROM golang:1.25 AS cloche-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent

FROM %s
RUN apt-get update && apt-get install -y git nodejs npm && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=cloche-builder /cloche-agent /usr/local/bin/cloche-agent
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace
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

func cmdInit(args []string) {
	workflow := "develop"
	image := "ubuntu:24.04"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflow = args[i]
			}
		case "--image":
			if i+1 < len(args) {
				i++
				image = args[i]
			}
		}
	}

	workflowFile := workflow + ".cloche"

	// Refuse to overwrite existing workflow
	if _, err := os.Stat(workflowFile); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists\n", workflowFile)
		os.Exit(1)
	}

	// Create prompts directory
	if err := os.MkdirAll("prompts", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating prompts/: %v\n", err)
		os.Exit(1)
	}

	// Write all files, skipping any that already exist
	files := []struct{ path, content string }{
		{workflowFile, fmt.Sprintf(workflowTemplate, workflow)},
		{"Dockerfile", fmt.Sprintf(dockerfileTemplate, image)},
		{"prompts/implement.md", implementPrompt},
		{"prompts/fix.md", fixPrompt},
	}

	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			fmt.Fprintf(os.Stderr, "  skip %s (already exists)\n", f.path)
			continue
		}
		if err := os.WriteFile(f.path, []byte(f.content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", f.path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "  create %s\n", f.path)
	}

	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "\nInitialized Cloche project in %s\n", filepath.Base(cwd))
	fmt.Fprintf(os.Stderr, "\nNext steps:\n")
	fmt.Fprintf(os.Stderr, "  1. Edit %s — change the test command for your project\n", workflowFile)
	fmt.Fprintf(os.Stderr, "  2. Edit Dockerfile — add your project's dependencies\n")
	fmt.Fprintf(os.Stderr, "  3. docker build -t cloche-agent .\n")
	fmt.Fprintf(os.Stderr, "  4. cloche run --workflow %s --prompt \"...\"\n", workflow)
}
