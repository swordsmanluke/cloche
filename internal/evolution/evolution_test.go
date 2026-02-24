package evolution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// --- Reflector tests ---

func TestReflectorExtractsLessons(t *testing.T) {
	lessonsJSON, _ := json.Marshal(map[string]any{
		"lessons": []map[string]any{
			{
				"id":               "lesson-001",
				"category":         "prompt_improvement",
				"target":           "prompts/implement.md",
				"insight":          "Agent produces unsanitized HTML",
				"suggested_action": "Add sanitization rule",
				"evidence":         []string{"run-1", "run-2"},
				"confidence":       "high",
			},
		},
	})

	llm := &fakeLLM{response: string(lessonsJSON)}
	r := &Reflector{LLM: llm, MinConfidence: "medium"}

	data := &CollectedData{WorkflowName: "develop", KnowledgeBase: "# KB\n"}
	lessons, err := r.Reflect(context.Background(), data, "bug")
	require.NoError(t, err)
	require.Len(t, lessons, 1)
	assert.Equal(t, "prompt_improvement", lessons[0].Category)
	assert.Equal(t, "high", lessons[0].Confidence)
}

func TestReflectorFiltersLowConfidence(t *testing.T) {
	lessonsJSON, _ := json.Marshal(map[string]any{
		"lessons": []map[string]any{
			{"id": "L1", "category": "new_step", "confidence": "low"},
			{"id": "L2", "category": "prompt_improvement", "confidence": "high"},
		},
	})

	llm := &fakeLLM{response: string(lessonsJSON)}
	r := &Reflector{LLM: llm, MinConfidence: "medium"}

	lessons, err := r.Reflect(context.Background(), &CollectedData{}, "bug")
	require.NoError(t, err)
	assert.Len(t, lessons, 1)
	assert.Equal(t, "L2", lessons[0].ID)
}

func TestConfidenceLevel(t *testing.T) {
	assert.Equal(t, 3, confidenceLevel("high"))
	assert.Equal(t, 2, confidenceLevel("medium"))
	assert.Equal(t, 1, confidenceLevel("low"))
	assert.Equal(t, 0, confidenceLevel(""))
}

// --- Audit Logger tests ---

func TestAuditLoggerAppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution"), 0755)

	logger := &AuditLogger{ProjectDir: dir}

	result1 := &EvolutionResult{
		ID:           "evo-1",
		WorkflowName: "develop",
		Changes:      []Change{{Type: "prompt_update", File: "prompts/impl.md"}},
	}
	require.NoError(t, logger.Log(result1))

	result2 := &EvolutionResult{
		ID:           "evo-2",
		WorkflowName: "develop",
		Changes:      []Change{{Type: "add_step", File: "develop.cloche"}},
	}
	require.NoError(t, logger.Log(result2))

	content, err := os.ReadFile(filepath.Join(dir, ".cloche", "evolution", "log.jsonl"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 2)

	var entry1 EvolutionResult
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1))
	assert.Equal(t, "evo-1", entry1.ID)
}

func TestAuditLoggerSnapshot(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.MkdirAll(filepath.Join(dir, "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, "prompts", "implement.md"), []byte("original content"), 0644)

	logger := &AuditLogger{ProjectDir: dir}
	snapName, err := logger.Snapshot("prompts/implement.md")
	require.NoError(t, err)
	assert.NotEmpty(t, snapName)

	snapPath := filepath.Join(dir, ".cloche", "evolution", "snapshots", snapName)
	content, err := os.ReadFile(snapPath)
	require.NoError(t, err)
	assert.Equal(t, "original content", string(content))
}

func TestAuditLoggerUpdatesKnowledge(t *testing.T) {
	dir := t.TempDir()
	kbDir := filepath.Join(dir, ".cloche", "evolution", "knowledge")
	os.MkdirAll(kbDir, 0755)
	os.WriteFile(filepath.Join(kbDir, "develop.md"), []byte("# Knowledge Base: develop workflow\n"), 0644)

	logger := &AuditLogger{ProjectDir: dir}
	lessons := []Lesson{
		{
			ID:              "P001",
			Category:        "prompt_improvement",
			Confidence:      "high",
			Insight:         "Always sanitize HTML inputs",
			SuggestedAction: "Add rule to implement prompt",
			Evidence:        []string{"run-1", "run-2"},
		},
	}

	require.NoError(t, logger.UpdateKnowledge("develop", lessons))

	content, err := os.ReadFile(filepath.Join(kbDir, "develop.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "[P001]")
	assert.Contains(t, string(content), "sanitize HTML")
	assert.Contains(t, string(content), "run-1, run-2")
}

// --- LLM Client tests ---

func TestCommandLLMClient(t *testing.T) {
	c := &CommandLLMClient{Command: "cat", Args: []string{}}
	result, err := c.Complete(context.Background(), "system", "user prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "user prompt")
	assert.Contains(t, result, "system")
}
