package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/config"
)

const initLLMTimeout = 30 * time.Second

// projectContextFiles are read and included in the LLM prompt to help it understand the project.
var projectContextFiles = []string{
	"package.json",
	"go.mod",
	"Cargo.toml",
	"pyproject.toml",
	"setup.py",
	"requirements.txt",
	"Makefile",
	"Gemfile",
	"pom.xml",
	"build.gradle",
}

// initTemplatePaths returns the generated files that may contain TODO(cloche-init) placeholders
// for the given workflow name. Only these paths are written back from LLM output.
func initTemplatePaths(workflow string) []string {
	return []string{
		filepath.Join(".cloche", workflow+".cloche"),
		filepath.Join(".cloche", "Dockerfile"),
		filepath.Join(".cloche", "prompts", "implement.md"),
	}
}

const initSystemPrompt = `You are a developer tooling assistant helping to configure a Cloche project.
Cloche is a system for running coding agents in containers.
Your job is to analyze the project files and fill in configuration templates accurately.`

// resolveLLMCommand returns the LLM command to use for the init phase.
// Priority: explicit agentCommand > CLOCHE_AGENT_COMMAND env > global config llm_command > claude on PATH.
func resolveLLMCommand(agentCommand string) (string, bool) {
	if agentCommand != "" {
		return agentCommand, true
	}
	if cmd := os.Getenv("CLOCHE_AGENT_COMMAND"); cmd != "" {
		return cmd, true
	}
	if cfg, err := config.LoadGlobal(); err == nil && cfg.Daemon.LLMCommand != "" {
		return cfg.Daemon.LLMCommand, true
	}
	if _, err := exec.LookPath("claude"); err == nil {
		return "claude", true
	}
	return "", false
}

// collectProjectContext gathers the project file listing and key project files for the LLM prompt.
func collectProjectContext() string {
	var sb strings.Builder

	entries, err := os.ReadDir(".")
	if err == nil {
		sb.WriteString("## Project root contents\n\n")
		for _, e := range entries {
			name := e.Name()
			if name == "node_modules" || name == ".git" {
				continue
			}
			if e.IsDir() {
				sb.WriteString("  " + name + "/\n")
			} else {
				sb.WriteString("  " + name + "\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Key project files\n\n")
	found := false
	for _, f := range projectContextFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		found = true
		sb.WriteString("### " + f + "\n\n```\n")
		sb.WriteString(string(data))
		if !strings.HasSuffix(string(data), "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n\n")
	}
	if !found {
		sb.WriteString("(none found)\n\n")
	}

	return sb.String()
}

// readInitTemplates returns the content of init template files that have TODO(cloche-init) placeholders.
func readInitTemplates(paths []string) map[string]string {
	result := make(map[string]string)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "TODO(cloche-init)") {
			result[path] = string(data)
		}
	}
	return result
}

// buildInitPrompt constructs the LLM prompt for filling in placeholders.
func buildInitPrompt(projectCtx string, paths []string, templates map[string]string) string {
	var sb strings.Builder

	sb.WriteString(projectCtx)
	sb.WriteString("## Template files with TODO(cloche-init) placeholders\n\n")
	for _, path := range paths {
		content, ok := templates[path]
		if !ok {
			continue
		}
		sb.WriteString("### " + path + "\n\n```\n")
		sb.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n\n")
	}

	sb.WriteString("Fill in the TODO(cloche-init) placeholders in the template files above based on this project.\n\n")
	sb.WriteString("For each file that needs updating, output the complete updated file content in a fenced code block with the file path as the info string. Example:\n\n")
	sb.WriteString("```" + `.cloche/Dockerfile
FROM cloche-agent:latest
USER root

RUN apt-get update && apt-get install -y python3 python3-pip

USER agent
` + "```\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Only output files that contain TODO(cloche-init) placeholders\n")
	sb.WriteString("- Output the complete file content, not just the changed sections\n")
	sb.WriteString("- Replace all TODO(cloche-init) blocks with real content for this project\n")
	sb.WriteString("- Keep all non-placeholder content exactly as-is\n")
	sb.WriteString("- For the .cloche workflow file: set the test step run command to the correct test command\n")
	sb.WriteString("- For .cloche/Dockerfile: add appropriate dependency installation for this project's runtime\n")
	sb.WriteString("- For .cloche/prompts/implement.md: add project-specific context (language, frameworks, conventions)\n")

	return sb.String()
}

// parseInitResponse extracts fenced code blocks with file paths from the LLM response.
// Returns a map of filepath -> content.
func parseInitResponse(response string) map[string]string {
	result := make(map[string]string)

	lines := strings.Split(response, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "```") {
			info := strings.TrimSpace(line[3:])
			// Treat as a file path if it looks like one (contains '/' or '.', no spaces)
			if info != "" && (strings.Contains(info, "/") || strings.Contains(info, ".")) && !strings.Contains(info, " ") {
				i++
				var contentLines []string
				for i < len(lines) {
					if strings.TrimSpace(lines[i]) == "```" {
						break
					}
					contentLines = append(contentLines, lines[i])
					i++
				}
				content := strings.Join(contentLines, "\n")
				if !strings.HasSuffix(content, "\n") {
					content += "\n"
				}
				result[info] = content
			}
		}
		i++
	}

	return result
}

// runLLMInitPhase invokes the LLM to fill in TODO(cloche-init) placeholders.
// Failures are non-fatal: a warning is printed and init continues normally.
func runLLMInitPhase(agentCommand, workflow string) {
	cmd, ok := resolveLLMCommand(agentCommand)
	if !ok {
		fmt.Fprintf(os.Stderr, "\nNo LLM client available — fill in TODO(cloche-init) placeholders manually.\n")
		fmt.Fprintf(os.Stderr, "Tip: install claude or set CLOCHE_AGENT_COMMAND to enable LLM-assisted init.\n")
		return
	}

	paths := initTemplatePaths(workflow)
	templates := readInitTemplates(paths)
	if len(templates) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "\nAnalyzing project with LLM (%s)...\n", cmd)

	projectCtx := collectProjectContext()
	prompt := buildInitPrompt(projectCtx, paths, templates)

	ctx, cancel := context.WithTimeout(context.Background(), initLLMTimeout)
	defer cancel()

	parts := strings.Fields(cmd)
	command := parts[0]
	args := append([]string(nil), parts[1:]...)
	args = append(args, "-p", "--system-prompt", initSystemPrompt, "--output-format", "text")

	execCmd := exec.CommandContext(ctx, command, args...)
	execCmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	// Strip CLAUDECODE so Claude doesn't refuse to start when inside a Claude Code session.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			execCmd.Env = append(execCmd.Env, e)
		}
	}

	if err := execCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: LLM init phase failed (%v) — fill in TODO(cloche-init) placeholders manually.\n", err)
		return
	}

	response := strings.TrimSpace(stdout.String())
	if response == "" {
		fmt.Fprintf(os.Stderr, "warning: LLM returned empty response — fill in TODO(cloche-init) placeholders manually.\n")
		return
	}

	updates := parseInitResponse(response)
	if len(updates) == 0 {
		fmt.Fprintf(os.Stderr, "warning: could not parse LLM response — fill in TODO(cloche-init) placeholders manually.\n")
		return
	}

	allowed := make(map[string]bool, len(paths))
	for _, p := range paths {
		allowed[p] = true
	}

	wrote := 0
	for path, content := range updates {
		if !allowed[path] {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write %s: %v\n", path, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  update %s\n", path)
		wrote++
	}

	if wrote == 0 {
		fmt.Fprintf(os.Stderr, "warning: LLM output did not match any template files — fill in TODO(cloche-init) placeholders manually.\n")
	}
}
