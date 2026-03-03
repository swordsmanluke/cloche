package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLM returns a fixed response for testing.
type fakeLLM struct {
	response    string
	err         error
	lastSystem  string
	lastUser    string
}

func (f *fakeLLM) Complete(_ context.Context, system, user string) (string, error) {
	f.lastSystem = system
	f.lastUser = user
	return f.response, f.err
}

func TestLLMPromptGeneratorBasic(t *testing.T) {
	llm := &fakeLLM{response: "Implement the login page with email and password fields."}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{
		ID:          "task-1",
		Title:       "Add login page",
		Description: "Create a login page with email and password fields",
		Labels:      []string{"frontend", "auth"},
		Priority:    1,
	}

	prompt, err := gen.Generate(context.Background(), task, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "Implement the login page with email and password fields.", prompt)

	// Verify the task info was passed to the LLM
	assert.Contains(t, llm.lastUser, "task-1")
	assert.Contains(t, llm.lastUser, "Add login page")
	assert.Contains(t, llm.lastUser, "email and password fields")
	assert.Contains(t, llm.lastUser, "frontend, auth")
}

func TestLLMPromptGeneratorWithAcceptance(t *testing.T) {
	llm := &fakeLLM{response: "Generated prompt with acceptance criteria."}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{
		ID:         "task-2",
		Title:      "Add form validation",
		Acceptance: "- Email must be valid\n- Password must be 8+ chars",
	}

	_, err := gen.Generate(context.Background(), task, t.TempDir())
	require.NoError(t, err)

	assert.Contains(t, llm.lastUser, "Acceptance Criteria")
	assert.Contains(t, llm.lastUser, "Email must be valid")
}

func TestLLMPromptGeneratorWithProjectContext(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# My Project\nUse Go. Follow hexagonal arch."), 0644)
	os.MkdirAll(filepath.Join(dir, "cmd"), 0755)
	os.MkdirAll(filepath.Join(dir, "internal"), 0755)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp"), 0644)

	llm := &fakeLLM{response: "Implementation prompt with context."}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{
		ID:    "task-3",
		Title: "Add health endpoint",
	}

	_, err := gen.Generate(context.Background(), task, dir)
	require.NoError(t, err)

	// CLAUDE.md content should be included
	assert.Contains(t, llm.lastUser, "CLAUDE.md")
	assert.Contains(t, llm.lastUser, "hexagonal arch")

	// Project structure should be included
	assert.Contains(t, llm.lastUser, "Project Structure")
	assert.Contains(t, llm.lastUser, "cmd/")
	assert.Contains(t, llm.lastUser, "internal/")
}

func TestLLMPromptGeneratorSystemPromptStructure(t *testing.T) {
	llm := &fakeLLM{response: "some prompt"}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{ID: "t1", Title: "Test task"}
	_, err := gen.Generate(context.Background(), task, t.TempDir())
	require.NoError(t, err)

	// System prompt should instruct the LLM on how to generate prompts
	assert.Contains(t, llm.lastSystem, "coding agent")
	assert.Contains(t, llm.lastSystem, "cloche run --prompt")
	assert.Contains(t, llm.lastSystem, "Acceptance criteria")
}

func TestLLMPromptGeneratorLLMError(t *testing.T) {
	llm := &fakeLLM{err: fmt.Errorf("connection refused")}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{ID: "t1", Title: "Test task"}
	_, err := gen.Generate(context.Background(), task, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt generation LLM call")
}

func TestLLMPromptGeneratorEmptyResponse(t *testing.T) {
	llm := &fakeLLM{response: "  "}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{ID: "t1", Title: "Test task"}
	_, err := gen.Generate(context.Background(), task, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty prompt")
}

func TestLLMPromptGeneratorMinimalTask(t *testing.T) {
	llm := &fakeLLM{response: "Implement the feature."}
	gen := &LLMPromptGenerator{LLM: llm}

	// Task with only required fields
	task := ports.TrackerTask{
		ID:    "minimal",
		Title: "Do the thing",
	}

	prompt, err := gen.Generate(context.Background(), task, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "Implement the feature.", prompt)

	// Should not contain optional sections
	assert.NotContains(t, llm.lastUser, "Acceptance Criteria")
	assert.NotContains(t, llm.lastUser, "Labels")
}

func TestLLMPromptGeneratorPriorityZeroOmitted(t *testing.T) {
	llm := &fakeLLM{response: "prompt"}
	gen := &LLMPromptGenerator{LLM: llm}

	task := ports.TrackerTask{
		ID:       "t1",
		Title:    "Test",
		Priority: 0,
	}

	_, err := gen.Generate(context.Background(), task, t.TempDir())
	require.NoError(t, err)
	assert.NotContains(t, llm.lastUser, "Priority")
}

func TestCommandLLMClientWithMockCommand(t *testing.T) {
	// Use echo as a mock LLM command — it ignores stdin and prints args
	c := &CommandLLMClient{Command: "cat", Args: []string{}}
	result, err := c.Complete(context.Background(), "system prompt", "user prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "system prompt")
	assert.Contains(t, result, "user prompt")
}

func TestCommandLLMClientFailure(t *testing.T) {
	c := &CommandLLMClient{Command: "false", Args: []string{}}
	_, err := c.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM command failed")
}

func TestNewCommandLLMClientFromEnv(t *testing.T) {
	// Test default (no env var set)
	os.Unsetenv("CLOCHE_LLM_COMMAND")
	c := NewCommandLLMClientFromEnv()
	assert.Equal(t, "claude", c.Command)
	assert.Equal(t, []string{"-p"}, c.Args)

	// Test with env var
	t.Setenv("CLOCHE_LLM_COMMAND", "my-llm --model gpt4 --json")
	c = NewCommandLLMClientFromEnv()
	assert.Equal(t, "my-llm", c.Command)
	assert.Equal(t, []string{"--model", "gpt4", "--json"}, c.Args)
}

func TestReadFileIfExists(t *testing.T) {
	dir := t.TempDir()

	// Non-existent file returns empty
	assert.Empty(t, readFileIfExists(filepath.Join(dir, "nope")))

	// Existing file returns content
	os.WriteFile(filepath.Join(dir, "test.md"), []byte("hello"), 0644)
	assert.Equal(t, "hello", readFileIfExists(filepath.Join(dir, "test.md")))

	// Large file is truncated
	big := strings.Repeat("x", 5000)
	os.WriteFile(filepath.Join(dir, "big.md"), []byte(big), 0644)
	content := readFileIfExists(filepath.Join(dir, "big.md"))
	assert.True(t, len(content) < 5000)
	assert.True(t, strings.HasSuffix(content, "...(truncated)"))
}

func TestReadProjectStructure(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cmd"), 0755)
	os.MkdirAll(filepath.Join(dir, "internal"), 0755)
	os.MkdirAll(filepath.Join(dir, ".git"), 0755) // hidden, should be excluded
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0644)

	structure := readProjectStructure(dir)
	assert.Contains(t, structure, "cmd/")
	assert.Contains(t, structure, "internal/")
	assert.Contains(t, structure, "go.mod")
	assert.NotContains(t, structure, ".git")
}

func TestGatherProjectContext(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Project instructions"), 0644)

	ctx := gatherProjectContext(dir)
	assert.Contains(t, ctx, "CLAUDE.md")
	assert.Contains(t, ctx, "Project instructions")
}

func TestReadGitLog(t *testing.T) {
	// If git is available, it should return something (or empty string) without error
	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	// Non-git directory returns empty
	dir := t.TempDir()
	result := readGitLog(dir)
	assert.Empty(t, result)
}

// TestPromptGeneratorInterface verifies LLMPromptGenerator satisfies the interface.
func TestPromptGeneratorInterface(t *testing.T) {
	var _ PromptGenerator = &LLMPromptGenerator{}
}

// TestLLMClientInterface verifies CommandLLMClient satisfies the interface.
func TestLLMClientInterface(t *testing.T) {
	var _ LLMClient = &CommandLLMClient{}
}
