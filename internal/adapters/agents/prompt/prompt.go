package prompt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
)

// defaultAgentArgs maps known agent commands to their default arguments.
// Commands not in this map receive no default arguments (prompt on stdin only).
var defaultAgentArgs = map[string][]string{
	"claude":  {"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"},
	"gemini":  {},
	"codex":   {},
	"aider":   {"--yes-always", "--message"},
}

type Adapter struct {
	Commands     []string            // ordered fallback chain of agent commands
	ExplicitArgs []string            // if non-nil, overrides default args for all commands
	AgentArgs    map[string][]string // per-agent arg overrides (e.g. "gemini" -> ["--model", "..."])
	RunID        string
	StatusWriter *protocol.StatusWriter // optional: streams live output lines
}

func New() *Adapter {
	return &Adapter{
		Commands: []string{"claude"},
	}
}

func (a *Adapter) Name() string {
	return "prompt"
}

// argsFor returns the arguments for the given command. Priority:
//  1. ExplicitArgs (overrides everything, applies to all commands)
//  2. AgentArgs[command] exact match, then basename match
//  3. defaultAgentArgs[command] exact match, then basename match
func (a *Adapter) argsFor(command string) []string {
	if a.ExplicitArgs != nil {
		return a.ExplicitArgs
	}
	base := filepath.Base(command)
	if a.AgentArgs != nil {
		if args, ok := a.AgentArgs[command]; ok {
			return args
		}
		if args, ok := a.AgentArgs[base]; ok {
			return args
		}
	}
	if args, ok := defaultAgentArgs[command]; ok {
		return args
	}
	if args, ok := defaultAgentArgs[base]; ok {
		return args
	}
	return nil
}

// KnownAgents returns the names of agents with built-in default arguments.
func KnownAgents() []string {
	agents := make([]string, 0, len(defaultAgentArgs))
	for name := range defaultAgentArgs {
		agents = append(agents, name)
	}
	return agents
}

// ParseCommands splits a comma-separated agent_command string into individual
// command names, trimming whitespace from each entry.
func ParseCommands(s string) []string {
	parts := strings.Split(s, ",")
	var cmds []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cmds = append(cmds, p)
		}
	}
	return cmds
}

func (a *Adapter) Execute(ctx context.Context, step *domain.Step, workDir string) (string, error) {
	// Check attempt count for retry limiting
	if maxStr, ok := step.Config["max_attempts"]; ok {
		max, err := strconv.Atoi(maxStr)
		if err == nil {
			count := readAttemptCount(workDir, step.Name)
			if count >= max {
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

	// Try each command in the fallback chain
	var lastResult string
	var lastStdout []byte
	var lastErr error
	ran := false

	for i, command := range a.Commands {
		if a.StatusWriter != nil && len(a.Commands) > 1 {
			if i == 0 {
				a.StatusWriter.Log(step.Name, fmt.Sprintf("[agent] trying %s", command))
			} else {
				a.StatusWriter.Log(step.Name, fmt.Sprintf("[agent] falling back to %s", command))
			}
		}

		result, stdout, fallbackErr := a.tryCommand(ctx, command, fullPrompt, workDir, step.Name)
		lastResult = result
		lastStdout = stdout
		lastErr = fallbackErr

		if fallbackErr == nil {
			// Definitive result — agent completed (exit 0 or exit non-zero with marker)
			break
		}
		if stdout != nil {
			ran = true
		}
		if a.StatusWriter != nil && len(a.Commands) > 1 && i < len(a.Commands)-1 {
			a.StatusWriter.Log(step.Name, fmt.Sprintf("[agent] %s failed: %v", command, fallbackErr))
		}
		// Fallback-eligible — try next command
	}

	if lastErr != nil && !ran && lastStdout == nil {
		// All commands failed to produce any output (e.g., all not found)
		return "", lastErr
	}

	result := lastResult
	if lastErr != nil {
		// Last command in chain crashed without a result marker
		result = "fail"
	}

	// Write output file
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(outputDir, step.Name+".log"), lastStdout, 0644)
	}
	protocol.AppendHistory(workDir, step.Name, result, true, nil)
	return result, nil
}

// tryCommand executes a single agent command and returns:
//   - result: the step result name (e.g. "success", "fail")
//   - stdout: captured stdout bytes
//   - fallbackErr: nil if the result is definitive, non-nil if fallback-eligible
//
// Fallback-eligible conditions:
//   - Command not found or failed to start (stdout will be nil)
//   - Command exited non-zero without a CLOCHE_RESULT marker
//
// Definitive (non-fallback) conditions:
//   - Command exited 0
//   - Command exited non-zero but produced a CLOCHE_RESULT marker
func (a *Adapter) tryCommand(ctx context.Context, command string, prompt string, workDir string, stepName string) (result string, stdout []byte, fallbackErr error) {
	args := a.argsFor(command)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)

	// If we have a StatusWriter, stream stdout line-by-line; otherwise buffer.
	if a.StatusWriter == nil {
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		runErr := cmd.Run()
		stdoutBytes := stdoutBuf.Bytes()
		return a.classifyResult(command, stdoutBytes, runErr)
	}

	// Streaming path: pipe stdout through a scanner so we can emit lines live.
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, fmt.Errorf("command %q stdout pipe: %w", command, err)
	}
	cmd.Stderr = nil // discard stderr

	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("command %q failed to start: %w", command, err)
	}

	// textBuf accumulates extracted text content for result extraction.
	// rawBuf accumulates raw stdout for the output log file.
	var textBuf, rawBuf bytes.Buffer
	// lineBuf accumulates text deltas into lines for streaming.
	var lineBuf strings.Builder

	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		raw := scanner.Bytes()
		rawBuf.Write(raw)
		rawBuf.WriteByte('\n')

		text := extractStreamText(raw)
		if text == "" {
			continue
		}
		textBuf.WriteString(text)

		// Stream complete lines to StatusWriter as they form.
		lineBuf.WriteString(text)
		for {
			s := lineBuf.String()
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				break
			}
			a.StatusWriter.Log(stepName, s[:idx])
			lineBuf.Reset()
			lineBuf.WriteString(s[idx+1:])
		}
	}
	// Flush any remaining partial line.
	if lineBuf.Len() > 0 {
		a.StatusWriter.Log(stepName, lineBuf.String())
	}
	if scanErr := scanner.Err(); scanErr != nil {
		a.StatusWriter.Log(stepName, fmt.Sprintf("[scan error: %v]", scanErr))
	}

	waitErr := cmd.Wait()
	// Prefer extracted text (stream-json) for result classification; fall back
	// to raw output for non-JSON commands (scripts, non-claude agents).
	classifyBuf := textBuf.Bytes()
	if len(bytes.TrimSpace(classifyBuf)) == 0 {
		classifyBuf = rawBuf.Bytes()
	}
	result, _, fallbackErr = a.classifyResult(command, classifyBuf, waitErr)
	return result, rawBuf.Bytes(), fallbackErr
}

// classifyResult interprets the command's exit status and stdout to determine
// the step result.
func (a *Adapter) classifyResult(command string, stdoutBytes []byte, runErr error) (string, []byte, error) {
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			// Command failed to start (not found, permission denied, etc.)
			return "", nil, fmt.Errorf("command %q failed to start: %w", command, runErr)
		}
		// Command ran but exited non-zero
		markerResult, _, found := protocol.ExtractResult(stdoutBytes)
		if found {
			return markerResult, stdoutBytes, nil
		}
		return "fail", stdoutBytes, fmt.Errorf("command %q exited with error: %w", command, runErr)
	}

	// Command succeeded (exit 0)
	if len(bytes.TrimSpace(stdoutBytes)) == 0 {
		return "fail", stdoutBytes, fmt.Errorf("command %q exited 0 but produced no output (auth/config issue?)", command)
	}
	markerResult, _, found := protocol.ExtractResult(stdoutBytes)
	result := "success"
	if found {
		result = markerResult
	}
	return result, stdoutBytes, nil
}

// extractStreamText parses a stream-json line and returns text content.
// It handles both content_block_delta (text_delta) events for streaming and
// result events for final output (which contains the CLOCHE_RESULT marker).
func extractStreamText(line []byte) string {
	// Fast path: only parse lines that could contain text we care about.
	if bytes.Contains(line, []byte(`"text_delta"`)) {
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(line, &event) == nil &&
			event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			return event.Delta.Text
		}
	}

	// Also extract the final result text (contains CLOCHE_RESULT marker).
	if bytes.Contains(line, []byte(`"result"`)) && bytes.Contains(line, []byte(`"subtype"`)) {
		var event struct {
			Type   string `json:"type"`
			Result string `json:"result"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type == "result" {
			return event.Result
		}
	}

	return ""
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

	// 3. Read feedback from .cloche/output/*.log (opt-in via step config)
	if step.Config["feedback"] == "true" {
		feedback := readFeedback(workDir)
		if feedback != "" {
			parts = append(parts, "## Validation Output\n"+feedback)
		}
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
