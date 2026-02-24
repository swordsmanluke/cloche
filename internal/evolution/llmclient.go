package evolution

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("LLM command failed: %w (stderr: %s)", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}
