package evolution

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CommandLLMClient invokes an LLM via a shell command.
type CommandLLMClient struct {
	Command string
	Args    []string
}

// Complete sends a system+user prompt to the LLM command and returns the response.
// It uses --print mode (-p) for non-interactive output and passes the system
// prompt via --system-prompt so the LLM can properly distinguish system
// instructions from user content.
func (c *CommandLLMClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	args := make([]string, len(c.Args))
	copy(args, c.Args)

	// Use print mode for non-interactive, structured output.
	args = append(args, "-p")

	// Pass system prompt as a separate flag so the LLM distinguishes it
	// from user content.
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	// Pass --output-format text to avoid JSON wrapper in the response.
	args = append(args, "--output-format", "text")

	cmd := exec.CommandContext(ctx, c.Command, args...)
	cmd.Stdin = strings.NewReader(userPrompt)

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
