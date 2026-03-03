package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/ports"
)

// LLMClient abstracts LLM calls for prompt generation.
type LLMClient interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// PromptGenerator generates implementation prompts from tracker tasks.
type PromptGenerator interface {
	Generate(ctx context.Context, task ports.TrackerTask, projectDir string) (string, error)
}

// CommandLLMClient invokes an LLM via a shell command.
type CommandLLMClient struct {
	Command string
	Args    []string
}

// Complete sends a system+user prompt to the LLM command and returns the response.
func (c *CommandLLMClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	prompt := systemPrompt + "\n\n" + userPrompt

	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	cmd.Stdin = strings.NewReader(prompt)

	// Strip CLAUDECODE env var so Claude Code doesn't refuse to start
	// when cloched is running inside a Claude Code session.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("LLM command failed: %w (stderr: %s)", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// LLMPromptGenerator uses an LLM to generate well-formed implementation prompts.
type LLMPromptGenerator struct {
	LLM LLMClient
}

// Generate produces an implementation prompt for the given task using project context.
func (g *LLMPromptGenerator) Generate(ctx context.Context, task ports.TrackerTask, projectDir string) (string, error) {
	projectContext := gatherProjectContext(projectDir)

	systemPrompt := `You are a prompt engineer for an autonomous coding agent.
Given a task description and project context, generate a clear, actionable implementation prompt.

The prompt you generate will be passed directly to a coding agent via "cloche run --prompt".
The agent will work autonomously in a container with the full project source.

Your output must be ONLY the implementation prompt text — no wrapping, no commentary, no markdown fences.

The prompt should include:
1. A clear statement of what to implement (from the task title and description)
2. Acceptance criteria (if provided)
3. Relevant project context that helps the agent understand conventions
4. Guidelines: follow existing patterns, write tests for new functionality, run tests before declaring success`

	var parts []string
	parts = append(parts, fmt.Sprintf("## Task\nID: %s\nTitle: %s", task.ID, task.Title))

	if task.Description != "" {
		parts = append(parts, "## Description\n"+task.Description)
	}

	if task.Acceptance != "" {
		parts = append(parts, "## Acceptance Criteria\n"+task.Acceptance)
	}

	if len(task.Labels) > 0 {
		parts = append(parts, "## Labels\n"+strings.Join(task.Labels, ", "))
	}

	if task.Priority > 0 {
		parts = append(parts, fmt.Sprintf("## Priority\n%d", task.Priority))
	}

	if projectContext != "" {
		parts = append(parts, "## Project Context\n"+projectContext)
	}

	userPrompt := strings.Join(parts, "\n\n")

	response, err := g.LLM.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("prompt generation LLM call: %w", err)
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return "", fmt.Errorf("LLM returned empty prompt")
	}

	return response, nil
}

// gatherProjectContext reads available project context files.
func gatherProjectContext(projectDir string) string {
	var sections []string

	// Read CLAUDE.md if present
	claudeMD := readFileIfExists(filepath.Join(projectDir, "CLAUDE.md"))
	if claudeMD != "" {
		sections = append(sections, "### CLAUDE.md\n"+claudeMD)
	}

	// Read recent git log
	gitLog := readGitLog(projectDir)
	if gitLog != "" {
		sections = append(sections, "### Recent Git History\n"+gitLog)
	}

	// Read project structure summary (top-level dirs and key files)
	structure := readProjectStructure(projectDir)
	if structure != "" {
		sections = append(sections, "### Project Structure\n"+structure)
	}

	return strings.Join(sections, "\n\n")
}

// readFileIfExists returns file contents or empty string.
func readFileIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	// Limit to a reasonable size for context
	if len(content) > 4000 {
		content = content[:4000] + "\n...(truncated)"
	}
	return content
}

// readGitLog returns recent git log entries, or empty string on error.
func readGitLog(projectDir string) string {
	cmd := exec.Command("git", "log", "--oneline", "-20")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readProjectStructure returns a summary of the project's top-level layout.
func readProjectStructure(projectDir string) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			lines = append(lines, name+"/")
		} else {
			lines = append(lines, name)
		}
	}
	return strings.Join(lines, "\n")
}

// NewCommandLLMClientFromEnv creates a CommandLLMClient from the CLOCHE_LLM_COMMAND
// environment variable. Falls back to "claude" with default args if unset.
func NewCommandLLMClientFromEnv() *CommandLLMClient {
	cmdStr := os.Getenv("CLOCHE_LLM_COMMAND")
	if cmdStr == "" {
		return &CommandLLMClient{
			Command: "claude",
			Args:    []string{"-p"},
		}
	}

	parts := strings.Fields(cmdStr)
	return &CommandLLMClient{
		Command: parts[0],
		Args:    parts[1:],
	}
}
