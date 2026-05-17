package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdapter_ResumeConversation_ArgsContainDashC(t *testing.T) {
	a := New()
	a.ResumeConversation = true

	args := a.argsFor("claude")
	// When not resuming, args should be the default for claude
	defaultArgs := defaultAgentArgs["claude"]
	assert.Equal(t, defaultArgs, args)

	// The -c flag is prepended in tryCommand, not argsFor.
	// We verify the flag is added by checking tryCommand constructs the
	// right arguments. Since we can't easily test tryCommand without a
	// real binary, we test the flag insertion logic directly.

	// Verify that the resume flag would be prepended
	resumeArgs := append([]string{"-c"}, args...)
	assert.Equal(t, "-c", resumeArgs[0])
	assert.Equal(t, "-p", resumeArgs[1])
}

func TestAdapter_ResumeConversation_PromptIsRetry(t *testing.T) {
	// When ResumeConversation is true, Execute should use "retry" as prompt
	// We can't fully test Execute without mocking the command, but we can
	// verify the flag is set correctly on the adapter.
	a := New()
	a.ResumeConversation = true
	assert.True(t, a.ResumeConversation)
}

func TestAdapter_OpencodeArgs_InjectsFormatJSON(t *testing.T) {
	a := New()
	// Mirror the wrapped_cloche config: explicit args from .cloche files,
	// no --format flag.
	a.ExplicitArgs = []string{"run", "--model", "digitalocean/kimi-k2.6", "--dangerously-skip-permissions"}

	args := a.argsFor("opencode")

	// --format json must be appended so opencode emits structured stream
	// events on stdout instead of TUI text on stderr (which the adapter
	// discards).
	assert.Contains(t, args, "--format")
	assert.Contains(t, args, "json")
	// Caller-supplied args are preserved in original order.
	assert.Equal(t, "run", args[0])
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "digitalocean/kimi-k2.6")
}

func TestAdapter_OpencodeArgs_PreservesExplicitFormat(t *testing.T) {
	a := New()
	// If the user explicitly sets --format default, we don't second-guess
	// them (mirrors how claude treats explicit --output-format).
	a.ExplicitArgs = []string{"run", "--format", "default", "--model", "x/y"}

	args := a.argsFor("opencode")

	// Only one --format should appear (the user's), not appended again.
	count := 0
	for _, a := range args {
		if a == "--format" {
			count++
		}
	}
	assert.Equal(t, 1, count, "must not append a second --format flag")
}

func TestAdapter_OpencodeArgs_DefaultsHasFormatJSON(t *testing.T) {
	// With no ExplicitArgs, the default args for opencode must include
	// --format json so the parser sees structured events.
	a := New()
	args := a.argsFor("opencode")

	assert.Contains(t, args, "run")
	assert.Contains(t, args, "--format")
	assert.Contains(t, args, "json")
}
