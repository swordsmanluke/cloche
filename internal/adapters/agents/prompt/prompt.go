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
	"github.com/cloche-dev/cloche/internal/protocol"
)

// CapturedData holds data captured during agent step execution.
type CapturedData struct {
	PromptText    string
	AgentOutput   string
	AttemptNumber int
}

type Adapter struct {
	Command   string
	Args      []string
	RunID     string
	OnCapture func(CapturedData)
}

func New() *Adapter {
	return &Adapter{
		Command: "claude",
		Args:    []string{"-p", "--output-format", "text", "--dangerously-skip-permissions"},
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
				if a.OnCapture != nil {
					a.OnCapture(CapturedData{AttemptNumber: count})
				}
				return "give-up", nil
			}
		}
	}
	incrementAttemptCount(workDir, step.Name)

	// Build the full prompt
	fullPrompt, err := assemblePrompt(step, workDir, a.RunID)
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

	if runErr := cmd.Run(); runErr != nil {
		if _, ok := runErr.(*exec.ExitError); ok {
			markerResult, _, found := protocol.ExtractResult(stdout.Bytes())
			result := "fail"
			if found {
				result = markerResult
			}
			protocol.AppendHistory(workDir, step.Name, result, true, nil)
			if a.OnCapture != nil {
				a.OnCapture(CapturedData{
					PromptText:    fullPrompt,
					AgentOutput:   stdout.String(),
					AttemptNumber: readAttemptCount(workDir, step.Name),
				})
			}
			return result, nil
		}
		return "", runErr
	}

	markerResult, _, found := protocol.ExtractResult(stdout.Bytes())
	result := "success"
	if found {
		result = markerResult
	}
	protocol.AppendHistory(workDir, step.Name, result, true, nil)
	if a.OnCapture != nil {
		a.OnCapture(CapturedData{
			PromptText:    fullPrompt,
			AgentOutput:   stdout.String(),
			AttemptNumber: readAttemptCount(workDir, step.Name),
		})
	}
	return result, nil
}

func assemblePrompt(step *domain.Step, workDir, runID string) (string, error) {
	var parts []string

	// 1. Read system template from step config
	if tmpl, ok := step.Config["prompt"]; ok {
		content, err := resolveContent(tmpl, workDir)
		if err != nil {
			return "", fmt.Errorf("reading prompt template: %w", err)
		}
		parts = append(parts, content)
	}

	// 2. Read user prompt from .cloche/<run-id>/prompt.txt
	userPrompt := readUserPrompt(workDir, runID)
	if userPrompt != "" {
		parts = append(parts, "## User Request\n"+userPrompt)
	}

	// 3. Read feedback from .cloche/output/*.log
	feedback := readFeedback(workDir)
	if feedback != "" {
		parts = append(parts, "## Validation Output\n"+feedback)
	}

	// 4. Result selection instructions
	if len(step.Results) > 0 {
		var resultLines []string
		resultLines = append(resultLines, "## Result Selection")
		resultLines = append(resultLines, "When you are finished, output exactly one of the following on its own line:")
		for _, r := range step.Results {
			resultLines = append(resultLines, protocol.ResultPrefix+r)
		}
		parts = append(parts, strings.Join(resultLines, "\n"))
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

// readUserPrompt reads the user prompt from .cloche/<run-id>/prompt.txt.
func readUserPrompt(workDir, runID string) string {
	if runID == "" {
		return ""
	}
	path := filepath.Join(workDir, ".cloche", runID, "prompt.txt")
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return ""
}

func incrementAttemptCount(workDir, stepName string) {
	dir := filepath.Join(workDir, ".cloche", "attempt_count")
	_ = os.MkdirAll(dir, 0755)
	count := readAttemptCount(workDir, stepName) + 1
	_ = os.WriteFile(filepath.Join(dir, stepName), []byte(strconv.Itoa(count)), 0644)
}
