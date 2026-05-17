package prompt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	"claude":   {"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions", "--model", "sonnet"},
	"opencode": {"run", "--format", "json", "--dangerously-skip-permissions"},
}

type Adapter struct {
	Commands           []string // ordered fallback chain of agent commands
	ExplicitArgs       []string // if non-nil, overrides default args for all commands
	RunID              string
	TaskID             string                 // task ID for runtime state paths (.cloche/runs/<task-id>/)
	StatusWriter       *protocol.StatusWriter // optional: streams live output lines
	ResumeConversation bool                   // when true, resume previous conversation instead of starting new one
	UsageCommand       string                 // optional: shell command to run after step to capture token usage JSON
	PrevOutput         string                 // content of the immediate predecessor step's output log
	ExtraEnv           []string               // additional KEY=VALUE env vars injected into the agent process
}

func New() *Adapter {
	return &Adapter{
		Commands: []string{"claude"},
	}
}

func (a *Adapter) Name() string {
	return "prompt"
}

// argsFor returns the arguments for the given command. If ExplicitArgs is set,
// it is used as the base, but required flags for known agents are always
// included (e.g. --output-format stream-json for claude). Otherwise, known
// agents get their default args.
func (a *Adapter) argsFor(command string) []string {
	if a.ExplicitArgs != nil {
		args := a.ExplicitArgs
		// Ensure required flags for known agents are present.
		if command == "claude" {
			if !containsArg(args, "--output-format") {
				args = append(args, "--output-format", "stream-json")
			}
			if !containsArg(args, "--verbose") {
				args = append(args, "--verbose")
			}
		}
		if command == "opencode" {
			// opencode emits its TUI on stderr by default and only the final
			// model reply on stdout. --format json makes it emit structured
			// events (text deltas, tool_use, step_finish with usage) on
			// stdout so the streaming parser can pick them up.
			if !containsArg(args, "--format") {
				args = append(args, "--format", "json")
			}
		}
		return args
	}
	if args, ok := defaultAgentArgs[command]; ok {
		return args
	}
	return nil
}

// containsArg checks if an argument list contains a specific flag.
func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
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

func (a *Adapter) Execute(ctx context.Context, step *domain.Step, workDir string) (domain.StepResult, error) {
	// Check attempt count for retry limiting
	if maxStr, ok := step.Config["max_attempts"]; ok {
		max, err := strconv.Atoi(maxStr)
		if err == nil {
			count := readAttemptCount(workDir, a.TaskID, step.Name)
			if count >= max {
				return domain.StepResult{Result: "give-up"}, nil
			}
		}
	}
	incrementAttemptCount(workDir, a.TaskID, step.Name)

	// Build the full prompt
	var fullPrompt string
	if a.ResumeConversation {
		fullPrompt = "retry"
	} else {
		var err error
		fullPrompt, err = assemblePrompt(step, workDir, a.TaskID, a.PrevOutput)
		if err != nil {
			return domain.StepResult{}, fmt.Errorf("assembling prompt: %w", err)
		}
	}

	// Try each command in the fallback chain
	var lastResult string
	var lastStdout []byte
	var lastUsage *domain.TokenUsage
	var lastErr error
	var lastCommand string
	ran := false

	for _, command := range a.Commands {
		result, stdout, usage, fallbackErr := a.tryCommand(ctx, command, fullPrompt, workDir, step.Name)
		lastResult = result
		lastStdout = stdout
		lastUsage = usage
		lastErr = fallbackErr
		lastCommand = command

		if fallbackErr == nil {
			// Definitive result — agent completed (exit 0 or exit non-zero with marker)
			break
		}
		if stdout != nil {
			ran = true
		}
		// Fallback-eligible — try next command
	}

	if lastErr != nil && !ran && lastStdout == nil {
		// All commands failed to produce any output (e.g., all not found)
		return domain.StepResult{}, lastErr
	}

	result := lastResult
	if lastErr != nil {
		// Last command in chain crashed without a result marker
		result = "fail"
	}

	// If no usage was captured from the agent output stream, try usage_command.
	// Step config takes precedence over the adapter-level field.
	if lastUsage == nil {
		usageCmd := a.UsageCommand
		if v := step.Config["usage_command"]; v != "" {
			usageCmd = v
		}
		if usageCmd != "" {
			lastUsage = runUsageCommand(ctx, usageCmd, workDir)
			if lastUsage != nil {
				lastUsage.AgentName = lastCommand
			}
		}
	}

	// Reset attempt counter on success so give-up only triggers after
	// consecutive failures, not after successful fixes whose downstream
	// tests fail for unrelated reasons.
	if result == "success" {
		resetAttemptCount(workDir, a.TaskID, step.Name)
	}

	// Append output file, preserving history across loop iterations.
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		appendStepLog(filepath.Join(outputDir, step.Name+".log"), lastStdout)
	}
	protocol.AppendHistory(workDir, step.Name, result, true, nil)
	return domain.StepResult{Result: result, Usage: lastUsage}, nil
}

// tryCommand executes a single agent command and returns:
//   - result: the step result name (e.g. "success", "fail")
//   - stdout: captured stdout bytes
//   - usage: token usage extracted from result event (nil if not available)
//   - fallbackErr: nil if the result is definitive, non-nil if fallback-eligible
//
// Fallback-eligible conditions:
//   - Command not found or failed to start (stdout will be nil)
//   - Command exited non-zero without a CLOCHE_RESULT marker
//
// Definitive (non-fallback) conditions:
//   - Command exited 0
//   - Command exited non-zero but produced a CLOCHE_RESULT marker
func (a *Adapter) tryCommand(ctx context.Context, command string, prompt string, workDir string, stepName string) (result string, stdout []byte, usage *domain.TokenUsage, fallbackErr error) {
	args := a.argsFor(command)
	// Resume mode: add -c flag to resume previous conversation
	if a.ResumeConversation {
		args = append([]string{"-c"}, args...)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	if len(a.ExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), a.ExtraEnv...)
	}

	// If we have a StatusWriter, stream stdout line-by-line; otherwise buffer.
	if a.StatusWriter == nil {
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		runErr := cmd.Run()
		stdoutBytes := stdoutBuf.Bytes()
		result, stdout, fallbackErr = a.classifyResult(command, stdoutBytes, runErr)
		usage = scanOutputForUsage(stdoutBytes)
		if usage != nil {
			usage.AgentName = command
		}
		return
	}

	// Streaming path: pipe stdout through a scanner so we can emit lines live.
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, nil, fmt.Errorf("command %q stdout pipe: %w", command, err)
	}
	cmd.Stderr = nil // discard stderr

	if err := cmd.Start(); err != nil {
		return "", nil, nil, fmt.Errorf("command %q failed to start: %w", command, err)
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

		// Capture token usage from result events.
		if u := extractResultUsage(raw); u != nil {
			u.AgentName = command
			usage = u
		}

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
	// Check raw output for agent-level errors (e.g. error_during_execution
	// from rate limits) before classifying the extracted text.
	if bytes.Contains(rawBuf.Bytes(), []byte(`"error_during_execution"`)) {
		return "fail", rawBuf.Bytes(), usage, fmt.Errorf("command %q reported error_during_execution", command)
	}
	// Prefer extracted text (stream-json) for result classification; fall back
	// to raw output for non-JSON commands (scripts, non-claude agents).
	classifyBuf := textBuf.Bytes()
	if len(bytes.TrimSpace(classifyBuf)) == 0 {
		classifyBuf = rawBuf.Bytes()
	}
	result, _, fallbackErr = a.classifyResult(command, classifyBuf, waitErr)
	return result, rawBuf.Bytes(), usage, fallbackErr
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
	// Detect agent errors that exit 0 but indicate failure in the stream
	// (e.g. rate limit exhaustion, internal errors).
	if bytes.Contains(stdoutBytes, []byte(`"error_during_execution"`)) {
		return "fail", stdoutBytes, fmt.Errorf("command %q reported error_during_execution", command)
	}
	markerResult, _, found := protocol.ExtractResult(stdoutBytes)
	result := "success"
	if found {
		result = markerResult
	}
	return result, stdoutBytes, nil
}

// runUsageCommand executes a shell command and parses its JSON output as token usage.
// Returns nil if the command fails or the output cannot be parsed.
func runUsageCommand(ctx context.Context, cmd string, workDir string) *domain.TokenUsage {
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
	if err != nil {
		log.Printf("warning: usage_command %q failed: %v", cmd, err)
		return nil
	}
	var data struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		log.Printf("warning: usage_command %q output not parseable: %v", cmd, err)
		return nil
	}
	return &domain.TokenUsage{
		InputTokens:  data.InputTokens,
		OutputTokens: data.OutputTokens,
	}
}

// extractStreamText parses a streaming-JSON event line and returns text content.
//
// Supported producers and their event types:
//   - Claude Code stream-json:
//       "assistant" — one per turn, message.content[] with text and tool_use blocks
//       "result"    — final event with the full result text (contains CLOCHE_RESULT marker)
//   - Opencode --format json:
//       "text"      — incremental text delta (part.text)
//       "tool_use"  — tool invocation (part.tool, part.state.input, part.state.output)
//
// For tool_use events we emit a short "--- Tool: name(arg) ---" summary so
// the live log mirrors the user's view of the agent's actions.
func extractStreamText(line []byte) string {
	// Fast path: skip lines that can't contain content we care about.
	if !bytes.Contains(line, []byte(`"type"`)) {
		return ""
	}

	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return ""
	}

	switch envelope.Type {
	case "assistant":
		return extractAssistantText(line)
	case "result":
		return extractResultText(line)
	case "text":
		return extractOpencodeText(line)
	case "tool_use":
		return extractOpencodeToolUse(line)
	}
	return ""
}

// extractOpencodeText extracts the text delta from an opencode "text" event.
// Shape: {"type":"text","part":{"text":"..."}}
//
// Opencode streams text as small deltas (often without trailing newlines).
// Unlike Claude's assistant events — which are complete turns and warrant
// a synthetic trailing \n — opencode deltas must be returned verbatim so the
// caller's line-buffer can reassemble them into natural lines.
func extractOpencodeText(line []byte) string {
	var event struct {
		Part struct {
			Text string `json:"text"`
		} `json:"part"`
	}
	if json.Unmarshal(line, &event) != nil {
		return ""
	}
	return event.Part.Text
}

// extractOpencodeToolUse renders an opencode "tool_use" event as a "--- Tool: ... ---"
// line, mirroring the format extractAssistantText uses for Claude tool_use blocks.
// Shape: {"type":"tool_use","part":{"tool":"write","state":{"input":{...}}}}
func extractOpencodeToolUse(line []byte) string {
	var event struct {
		Part struct {
			Tool  string `json:"tool"`
			State struct {
				Input json.RawMessage `json:"input"`
			} `json:"state"`
		} `json:"part"`
	}
	if json.Unmarshal(line, &event) != nil {
		return ""
	}
	if event.Part.Tool == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- Tool: ")
	b.WriteString(event.Part.Tool)
	b.WriteString(toolInputSummary(event.Part.State.Input))
	b.WriteString(" ---\n")
	return b.String()
}

// extractAssistantText extracts text and tool-use summaries from an assistant event.
func extractAssistantText(line []byte) string {
	var event struct {
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &event) != nil {
		return ""
	}

	var b strings.Builder
	for _, c := range event.Message.Content {
		switch c.Type {
		case "text":
			if c.Text != "" {
				b.WriteString(c.Text)
				if !strings.HasSuffix(c.Text, "\n") {
					b.WriteByte('\n')
				}
			}
		case "tool_use":
			b.WriteString("--- Tool: ")
			b.WriteString(c.Name)
			b.WriteString(toolInputSummary(c.Input))
			b.WriteString(" ---\n")
		}
	}
	return b.String()
}

// extractResultText extracts the final result text (contains CLOCHE_RESULT marker).
func extractResultText(line []byte) string {
	if !bytes.Contains(line, []byte(`"subtype"`)) {
		return ""
	}
	var event struct {
		Type   string `json:"type"`
		Result string `json:"result"`
	}
	if json.Unmarshal(line, &event) == nil && event.Type == "result" {
		return event.Result
	}
	return ""
}

// extractResultUsage parses a streaming-JSON event line and returns token
// usage if present. Returns nil if the line carries no recognised usage data.
//
// Supported event shapes:
//   - Claude "result": top-level `usage.{input_tokens,output_tokens}`
//   - Opencode "step_finish": nested `part.tokens.{input,output}`
func extractResultUsage(line []byte) *domain.TokenUsage {
	// Quick filter: Claude uses "usage", opencode uses "tokens" inside step_finish.
	if !bytes.Contains(line, []byte(`"usage"`)) && !bytes.Contains(line, []byte(`"tokens"`)) {
		return nil
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return nil
	}
	switch envelope.Type {
	case "result":
		var event struct {
			Usage *struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &event) != nil || event.Usage == nil {
			return nil
		}
		return &domain.TokenUsage{
			InputTokens:  event.Usage.InputTokens,
			OutputTokens: event.Usage.OutputTokens,
		}
	case "step_finish":
		var event struct {
			Part struct {
				Tokens *struct {
					Input  int64 `json:"input"`
					Output int64 `json:"output"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if json.Unmarshal(line, &event) != nil || event.Part.Tokens == nil {
			return nil
		}
		return &domain.TokenUsage{
			InputTokens:  event.Part.Tokens.Input,
			OutputTokens: event.Part.Tokens.Output,
		}
	}
	return nil
}

// scanOutputForUsage scans buffered (non-streaming) output for a result event
// containing token usage data. Returns the first usage found, or nil.
func scanOutputForUsage(output []byte) *domain.TokenUsage {
	for _, line := range bytes.Split(output, []byte("\n")) {
		if u := extractResultUsage(line); u != nil {
			return u
		}
	}
	return nil
}

// toolInputSummary returns a short parenthesized summary of a tool's input.
func toolInputSummary(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	// Pick the most informative single argument to show. The first two keys
	// in each pair are Claude/opencode variants of the same field (snake/
	// camelCase). Order matters: tools that take a path get the path shown
	// over a longer command field.
	for _, key := range []string{"file_path", "filePath", "command", "pattern", "prompt", "skill"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				if len(s) > 60 {
					s = s[:57] + "..."
				}
				return "('" + s + "')"
			}
		}
	}
	return ""
}

func assemblePrompt(step *domain.Step, workDir, taskID string, prevOutput string) (string, error) {
	var parts []string

	// Gather substitution values
	userPrompt := readUserPrompt(workDir, taskID)

	// 1. Read system template from step config
	if tmpl, ok := step.Config["prompt"]; ok {
		content, err := resolveContent(tmpl, workDir)
		if err != nil {
			return "", fmt.Errorf("reading prompt template: %w", err)
		}
		// Substitute template placeholders if present
		if strings.Contains(content, "{task_description}") {
			content = strings.ReplaceAll(content, "{task_description}", userPrompt)
			userPrompt = "" // consumed — don't append again
		}
		if strings.Contains(content, "{previous_output}") {
			content = strings.ReplaceAll(content, "{previous_output}", prevOutput)
		}
		parts = append(parts, content)
	}

	// 2. Append user prompt if not already substituted into template
	if userPrompt != "" {
		parts = append(parts, "## User Request\n"+userPrompt)
	}

	// 3. Result selection instructions
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


func readAttemptCount(workDir, taskID, stepName string) int {
	path := filepath.Join(workDir, ".cloche", "runs", taskID, "attempt_count", stepName)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

// readUserPrompt reads the user prompt from .cloche/runs/<task-id>/prompt.txt.
func readUserPrompt(workDir, taskID string) string {
	if taskID == "" {
		return ""
	}
	path := filepath.Join(workDir, ".cloche", "runs", taskID, "prompt.txt")
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return ""
}

func resetAttemptCount(workDir, taskID, stepName string) {
	path := filepath.Join(workDir, ".cloche", "runs", taskID, "attempt_count", stepName)
	_ = os.Remove(path)
}

func incrementAttemptCount(workDir, taskID, stepName string) {
	dir := filepath.Join(workDir, ".cloche", "runs", taskID, "attempt_count")
	_ = os.MkdirAll(dir, 0755)
	count := readAttemptCount(workDir, taskID, stepName) + 1
	_ = os.WriteFile(filepath.Join(dir, stepName), []byte(strconv.Itoa(count)), 0644)
}

// appendStepLog appends data to the step log file, preserving prior invocations.
func appendStepLog(path string, data []byte) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.Write(data)
	_ = f.Close()
}
