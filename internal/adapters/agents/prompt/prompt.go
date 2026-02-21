package prompt

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
)

type Adapter struct {
	Command string
	Args    []string
}

func New() *Adapter {
	return &Adapter{
		Command: "claude",
		Args:    []string{"-p", "--output-format", "text"},
	}
}

func (a *Adapter) Name() string {
	return "prompt"
}

func (a *Adapter) Execute(ctx context.Context, step *domain.Step, workDir string) (string, error) {
	// Check attempt count for retry limiting
	if maxStr, ok := step.Config["max_attempts"]; ok {
		max, err := strconv.Atoi(maxStr)
		if err == nil {
			count := readAttemptCount(workDir, step.Name)
			if count >= max {
				return resultOrDefault(step.Results, "give-up"), nil
			}
		}
	}
	incrementAttemptCount(workDir, step.Name)

	// Build the full prompt
	fullPrompt, err := assemblePrompt(step, workDir)
	if err != nil {
		return "", fmt.Errorf("assembling prompt: %w", err)
	}

	// Shell out to LLM command
	cmd := exec.CommandContext(ctx, a.Command, a.Args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(fullPrompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return resultOrDefault(step.Results, "fail"), nil
		}
		return "", err
	}

	return resultOrDefault(step.Results, "success"), nil
}

func assemblePrompt(step *domain.Step, workDir string) (string, error) {
	var parts []string

	// 1. Read system template from step config
	if tmpl, ok := step.Config["prompt"]; ok {
		content, err := resolveContent(tmpl, workDir)
		if err != nil {
			return "", fmt.Errorf("reading prompt template: %w", err)
		}
		parts = append(parts, content)
	}

	// 2. Read user prompt from .cloche/prompt.txt
	userPromptPath := filepath.Join(workDir, ".cloche", "prompt.txt")
	if data, err := os.ReadFile(userPromptPath); err == nil {
		parts = append(parts, "## User Request\n"+string(data))
	}

	// 3. Read feedback from .cloche/output/*.log
	feedback := readFeedback(workDir)
	if feedback != "" {
		parts = append(parts, "## Validation Output\n"+feedback)
	}

	return strings.Join(parts, "\n\n"), nil
}

// resolveContent handles file("path") syntax or returns the string directly.
func resolveContent(value string, workDir string) (string, error) {
	// Check for file("path") syntax from DSL parser
	if strings.HasPrefix(value, `file("`) && strings.HasSuffix(value, `")`) {
		path := value[6 : len(value)-2]
		data, err := os.ReadFile(filepath.Join(workDir, path))
		if err != nil {
			return "", fmt.Errorf("reading file %q: %w", path, err)
		}
		return string(data), nil
	}
	return value, nil
}

func readFeedback(workDir string) string {
	outputDir := filepath.Join(workDir, ".cloche", "output")
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return ""
	}

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(outputDir, entry.Name()))
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			stepName := strings.TrimSuffix(entry.Name(), ".log")
			parts = append(parts, fmt.Sprintf("### %s\n```\n%s\n```", stepName, content))
		}
	}

	return strings.Join(parts, "\n\n")
}

func readAttemptCount(workDir, stepName string) int {
	path := filepath.Join(workDir, ".cloche", "attempt_count", stepName)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func incrementAttemptCount(workDir, stepName string) {
	dir := filepath.Join(workDir, ".cloche", "attempt_count")
	_ = os.MkdirAll(dir, 0755)
	count := readAttemptCount(workDir, stepName) + 1
	_ = os.WriteFile(filepath.Join(dir, stepName), []byte(strconv.Itoa(count)), 0644)
}

func resultOrDefault(results []string, name string) string {
	for _, r := range results {
		if r == name {
			return r
		}
	}
	if len(results) > 0 {
		return results[0]
	}
	return name
}
