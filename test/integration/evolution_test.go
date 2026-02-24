package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLM returns responses in sequence.
type fakeLLM struct {
	responses []string
	idx       int
}

func (f *fakeLLM) Complete(ctx context.Context, system, user string) (string, error) {
	resp := f.responses[f.idx]
	f.idx++
	return resp, nil
}

func TestEvolutionPipelineIntegration(t *testing.T) {
	dir := t.TempDir()
	setupTestProject(t, dir)

	llm := &fakeLLM{
		responses: []string{
			// Classifier
			`{"classification": "bug"}`,
			// Reflector
			`{"lessons": [{"id": "L001", "category": "prompt_improvement", "target": "prompts/implement.md", "insight": "Test insight", "suggested_action": "Add a rule", "evidence": ["run-1"], "confidence": "high"}]}`,
			// Curator
			"Updated prompt content with new rule.\n",
		},
	}

	orch := evolution.NewOrchestrator(evolution.OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)

	// Verify changes were made
	assert.Len(t, result.Changes, 1)
	assert.Equal(t, "prompt_update", result.Changes[0].Type)

	// Verify audit trail
	logPath := filepath.Join(dir, ".cloche", "evolution", "log.jsonl")
	logContent, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logContent), "prompt_update")

	// Verify knowledge base was updated
	kbPath := filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md")
	kbContent, err := os.ReadFile(kbPath)
	require.NoError(t, err)
	assert.Contains(t, string(kbContent), "L001")

	// Verify snapshot was created
	snapDir := filepath.Join(dir, ".cloche", "evolution", "snapshots")
	entries, err := os.ReadDir(snapDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)
}

func setupTestProject(t *testing.T, dir string) {
	t.Helper()
	dirs := []string{
		".cloche/evolution/knowledge",
		".cloche/evolution/snapshots",
		"prompts",
	}
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0755))
	}

	files := map[string]string{
		".cloche/evolution/knowledge/develop.md": "# Knowledge Base: develop\n",
		"prompts/implement.md":                   "Write good code.\n",
		"develop.cloche": `workflow "develop" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  implement:success -> test
  test:success -> done
  test:fail -> abort
}`,
	}
	for path, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0644))
	}
}
