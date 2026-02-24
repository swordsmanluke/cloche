package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLM returns a fixed response for testing.
type fakeLLM struct {
	response string
}

func (f *fakeLLM) Complete(ctx context.Context, system, user string) (string, error) {
	return f.response, nil
}

// --- Collector tests ---

func TestCollectorGathersData(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md"),
		[]byte("# Knowledge Base\n"), 0644)
	os.WriteFile(filepath.Join(dir, "prompts", "implement.md"),
		[]byte("Write good code"), 0644)
	os.WriteFile(filepath.Join(dir, "develop.cloche"),
		[]byte(`workflow "develop" {
  step impl {
    prompt = file("prompts/implement.md")
    results = [success]
  }
  impl:success -> done
}`), 0644)

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "# Knowledge Base\n", data.KnowledgeBase)
	assert.Contains(t, data.CurrentPrompts, "prompts/implement.md")
	assert.Equal(t, "Write good code", data.CurrentPrompts["prompts/implement.md"])
	assert.NotEmpty(t, data.CurrentWorkflow)
	assert.Equal(t, dir, data.ProjectDir)
	assert.Equal(t, "develop", data.WorkflowName)
}

func TestCollectorNoKnowledgeBase(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "develop.cloche"),
		[]byte(`workflow "develop" { step s { run = "echo hi" results = [success] } s:success -> done }`), 0644)

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, data.KnowledgeBase)
}

func TestExtractPromptFiles(t *testing.T) {
	workflow := `step impl { prompt = file("prompts/implement.md") }
step fix { prompt = file("prompts/fix.md") }
step test { run = "make test" }`

	files := extractPromptFiles(workflow)
	assert.Len(t, files, 2)
	assert.Contains(t, files, "prompts/implement.md")
	assert.Contains(t, files, "prompts/fix.md")
}

// --- Classifier tests ---

func TestClassifierCategorizesRun(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{"bug", `{"classification": "bug"}`, "bug"},
		{"feature", `{"classification": "feature"}`, "feature"},
		{"feedback", `{"classification": "feedback"}`, "feedback"},
		{"enhancement", `{"classification": "enhancement"}`, "enhancement"},
		{"chore", `{"classification": "chore"}`, "chore"},
		{"invalid defaults to feature", `{"classification": "unknown"}`, "feature"},
		{"malformed JSON defaults to feature", `not json`, "feature"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &fakeLLM{response: tt.response}
			c := &Classifier{LLM: llm}
			result, err := c.Classify(context.Background(), "some prompt")
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
