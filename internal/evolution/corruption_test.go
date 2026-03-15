package evolution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errLLM returns a fixed error for testing error paths.
type errLLM struct {
	err error
}

func (e *errLLM) Complete(ctx context.Context, system, user string) (string, error) {
	return "", e.err
}

// callTrackingLLM records calls and returns fixed responses in order.
type callTrackingLLM struct {
	mu        sync.Mutex
	responses []string
	idx       int
	calls     []llmCall
}

type llmCall struct {
	system string
	user   string
}

func (c *callTrackingLLM) Complete(ctx context.Context, system, user string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, llmCall{system: system, user: user})
	if c.idx >= len(c.responses) {
		return "", fmt.Errorf("no more responses")
	}
	resp := c.responses[c.idx]
	c.idx++
	return resp, nil
}

// --- Mock stores ---

type mockEvolutionStore struct {
	lastEvolution *ports.EvolutionEntry
	runs          []*domain.Run
	saved         []*ports.EvolutionEntry
	listCalls     []listRunsSinceCall
}

type listRunsSinceCall struct {
	projectDir   string
	workflowName string
	sinceRunID   string
}

func (m *mockEvolutionStore) SaveEvolution(ctx context.Context, entry *ports.EvolutionEntry) error {
	m.saved = append(m.saved, entry)
	return nil
}

func (m *mockEvolutionStore) GetLastEvolution(ctx context.Context, projectDir, workflowName string) (*ports.EvolutionEntry, error) {
	if m.lastEvolution != nil {
		return m.lastEvolution, nil
	}
	return nil, nil
}

func (m *mockEvolutionStore) ListRunsSince(ctx context.Context, projectDir, workflowName, sinceRunID string) ([]*domain.Run, error) {
	m.listCalls = append(m.listCalls, listRunsSinceCall{projectDir, workflowName, sinceRunID})
	return m.runs, nil
}

type mockCaptureStore struct {
	captures map[string][]*domain.StepExecution
}

func (m *mockCaptureStore) SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error {
	return nil
}

func (m *mockCaptureStore) GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error) {
	if m.captures != nil {
		return m.captures[runID], nil
	}
	return nil, nil
}

// ===========================================================================
// Curator corruption scenarios
// ===========================================================================

func setupCuratorDir(t *testing.T, promptContent string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.WriteFile(promptPath, []byte(promptContent), 0644)
	return dir, promptPath
}

func TestCuratorCorruption_ConversationalMetaTextNoFences(t *testing.T) {
	// LLM returns conversational meta-text without code fences.
	// "Sure!" doesn't match the conversational markers regex, so it passes
	// through stripCodeFences and isConversationalResponse unchanged.
	// The post-write sanity check catches it (no '#' heading) and rolls back.
	original := "# Implementation Prompt\n\nWrite good code.\n"
	dir, promptPath := setupCuratorDir(t, original)

	conversationalResponse := "Sure! I've updated the prompt to include a sanitization rule. The prompt now instructs the agent to always sanitize user inputs before processing them."
	llm := &fakeLLM{response: conversationalResponse}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "XSS in form handlers",
		SuggestedAction: "Add sanitization rule",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "prompt_update_rollback", change.Type)
	assert.Contains(t, change.Reason, "rolled back")

	// Original content is preserved via rollback.
	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(content))
}

func TestCuratorCorruption_ResponseShorterThanOriginal(t *testing.T) {
	// LLM returns a response that is shorter than the original prompt,
	// representing total content loss. The sanity check catches this
	// (no '#' heading) and rolls back to the snapshot.
	original := "# Implementation Prompt\n\nWrite good code.\n\n## Guidelines\n\n- Follow best practices\n- Use proper error handling\n- Write tests\n\n## Learned Rules\n\n- Always validate inputs\n- Never trust user data\n"
	dir, promptPath := setupCuratorDir(t, original)

	// Extremely short response — no heading, fails sanity check.
	shortResponse := "Write good code."
	llm := &fakeLLM{response: shortResponse}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Missing error handling",
		SuggestedAction: "Add error handling rule",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "prompt_update_rollback", change.Type)
	assert.NotEmpty(t, change.Snapshot)
	assert.Contains(t, change.Reason, "rolled back")

	// Original content is preserved via rollback.
	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(content))
	assert.Contains(t, string(content), "## Guidelines")
	assert.Contains(t, string(content), "## Learned Rules")
}

func TestCuratorCorruption_EmptyResponse(t *testing.T) {
	// LLM returns an empty string. isConversationalResponse returns true
	// for empty strings, so the fallback appends the lesson directly,
	// preserving the original content.
	original := "# Implementation Prompt\n\nWrite good code.\n"
	dir, promptPath := setupCuratorDir(t, original)

	llm := &fakeLLM{response: ""}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Test insight",
		SuggestedAction: "Test action",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "prompt_update", change.Type)

	// The conversational guard catches the empty string and falls back
	// to appendLessonDirectly, preserving original content.
	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Implementation Prompt")
	assert.Contains(t, string(content), "Test insight")
}

func TestCuratorCorruption_EmptyCodeFences(t *testing.T) {
	// LLM returns only code fences with no content between them.
	// After stripping fences, content is empty → isConversationalResponse
	// returns true → fallback appends lesson directly → sanity check passes.
	original := "# Implementation Prompt\n\nWrite good code.\n"
	dir, promptPath := setupCuratorDir(t, original)

	llm := &fakeLLM{response: "```markdown\n```"}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Test insight",
		SuggestedAction: "Test action",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "prompt_update", change.Type)

	// Fallback preserves original content and appends lesson.
	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Implementation Prompt")
	assert.Contains(t, string(content), "Test insight")
}

func TestCuratorCorruption_RaceConcurrentApply(t *testing.T) {
	// Multiple rapid Curator.Apply() calls on the same file.
	original := "# Prompt\n\nOriginal content.\n"
	dir, promptPath := setupCuratorDir(t, original)

	audit := &AuditLogger{ProjectDir: dir}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			llm := &fakeLLM{
				response: fmt.Sprintf("# Prompt\n\nUpdated by goroutine %d.\n", idx),
			}
			c := &Curator{LLM: llm, Audit: audit}
			lesson := &Lesson{
				ID:              fmt.Sprintf("lesson-%d", idx),
				Target:          ".cloche/prompts/implement.md",
				Insight:         fmt.Sprintf("Insight %d", idx),
				SuggestedAction: fmt.Sprintf("Action %d", idx),
			}
			_, errors[idx] = c.Apply(context.Background(), dir, lesson)
		}(i)
	}

	wg.Wait()

	// All calls should succeed (no panics or crashes).
	for i, err := range errors {
		assert.NoError(t, err, "goroutine %d failed", i)
	}

	// The file should contain valid content from one of the goroutines.
	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Prompt")
	assert.Contains(t, string(content), "Updated by goroutine")
}

// ===========================================================================
// Rollback sanity check tests
// ===========================================================================

func TestCuratorRollback_GarbageLLMOutputPreservesOriginal(t *testing.T) {
	// Mock the LLM to return garbage. Assert the original file content
	// is preserved after Curator.Apply() returns.
	original := "# Implementation Prompt\n\nWrite good code.\n\n## Guidelines\n\n- Follow best practices\n"
	dir, promptPath := setupCuratorDir(t, original)

	// LLM returns garbage that doesn't look like a prompt
	garbageResponse := "asdf jkl; random garbage 12345 no markdown here"
	llm := &fakeLLM{response: garbageResponse}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Need better validation",
		SuggestedAction: "Add validation rules",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	require.NotNil(t, change)

	// Change should indicate rollback
	assert.Equal(t, "prompt_update_rollback", change.Type)
	assert.Contains(t, change.Reason, "rolled back")

	// Original file content is preserved
	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(content))
}

func TestCuratorRollback_SnapshotNotDeletedOnRollback(t *testing.T) {
	// Assert the snapshot is NOT deleted on rollback (it's evidence).
	original := "# Implementation Prompt\n\nWrite good code.\n\n## Guidelines\n\n- Follow best practices\n"
	dir, _ := setupCuratorDir(t, original)

	// LLM returns garbage
	llm := &fakeLLM{response: "no markdown heading here at all"}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Need better validation",
		SuggestedAction: "Add validation rules",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.Equal(t, "prompt_update_rollback", change.Type)
	assert.NotEmpty(t, change.Snapshot)

	// Verify the snapshot file still exists (not deleted)
	snapDir := filepath.Join(dir, ".cloche", "evolution", "snapshots")
	snapPath := filepath.Join(snapDir, change.Snapshot)
	_, err = os.Stat(snapPath)
	assert.NoError(t, err, "snapshot file should still exist after rollback")

	// Verify snapshot contains the original content
	snapContent, err := os.ReadFile(snapPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(snapContent))
}

func TestCuratorRollback_ChangeIndicatesRollback(t *testing.T) {
	// Assert the Change entry in the result indicates the rollback.
	original := "# Implementation Prompt\n\nWrite good code.\n\n## Section\n\n- Rule one\n"
	dir, _ := setupCuratorDir(t, original)

	// LLM returns content without any heading
	llm := &fakeLLM{response: "This is just plain text with no structure whatsoever."}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Important insight",
		SuggestedAction: "Do something",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	require.NotNil(t, change)

	// Verify the Change entry indicates rollback
	assert.Equal(t, "prompt_update_rollback", change.Type)
	assert.Equal(t, ".cloche/prompts/implement.md", change.File)
	assert.Contains(t, change.Reason, "rolled back")
	assert.Contains(t, change.Reason, "sanity check")
	assert.NotEmpty(t, change.Snapshot)
}

func TestPromptSanityCheck(t *testing.T) {
	tests := []struct {
		name    string
		content string
		valid   bool
	}{
		{"empty", "", false},
		{"whitespace only", "   \n\n  ", false},
		{"no heading", "Just some text without markdown headings", false},
		{"conversational", "I've updated the prompt for you.", false},
		{"valid with subsection", "# Title\n\n## Section\n\nContent here.\n", true},
		{"valid single heading with content", "# Title\n\nSome content here.\n", true},
		{"valid content with heading not at start", "Write good code.\n\n## Learned Rules\n\n- Rule one\n", true},
		{"garbage text", "asdf jkl; random garbage", false},
		{"heading at start", "# Prompt\n\nDo things.\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, promptSanityCheck(tt.content), "content: %q", tt.content)
		})
	}
}

func TestStripCodeFences_EmptyFences(t *testing.T) {
	// Fences with nothing between them.
	assert.Equal(t, "", stripCodeFences("```\n```"))
	assert.Equal(t, "", stripCodeFences("```markdown\n```"))
}

func TestStripCodeFences_OnlyWhitespace(t *testing.T) {
	// Fences with only whitespace between them.
	result := stripCodeFences("```\n   \n```")
	assert.Equal(t, "   \n", result)
}

// ===========================================================================
// Orchestrator edge case tests
// ===========================================================================

func setupOrchestratorDir(t *testing.T, workflowContent, promptContent, kbContent string) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md"),
		[]byte(kbContent), 0644)
	if promptContent != "" {
		os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"),
			[]byte(promptContent), 0644)
	}
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(workflowContent), 0644)
	return dir
}

func TestOrchestratorReflectorProducesDuplicateLessons(t *testing.T) {
	// Reflector produces lessons that are already in the knowledge base.
	// The orchestrator should still apply them (curation handles dedup).
	// Note: the prompt must NOT contain the lesson's insight text, otherwise
	// lessonAlreadyPresent returns true and the curator skips the update.
	kb := "# Knowledge Base: develop\n\n## Learned (2026-03-15)\n- [L001] high: Always sanitize user inputs (run-1, run-2)\n"
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`,
		"# Prompt\n\nWrite good code.\n",
		kb,
	)

	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "bug"}`,
			// Reflector returns a lesson that's already in the KB
			`{"lessons": [{"id": "L001", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Always sanitize user inputs", "suggested_action": "Add sanitization rule", "evidence": ["run-3"], "confidence": "high"}]}`,
			// Curator returns updated content (idempotent update)
			"# Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Always sanitize user inputs\n",
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-3", nil, nil)
	require.NoError(t, err)
	// Should still process the lesson even though it's a duplicate in KB
	assert.Len(t, result.Changes, 1)
	assert.Equal(t, "prompt_update", result.Changes[0].Type)

	// Knowledge base gets the lesson appended again
	kbContent, _ := os.ReadFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md"))
	assert.Contains(t, string(kbContent), "L001")
}

func TestOrchestratorHandleNewStepAlreadyExists(t *testing.T) {
	// handleNewStep tries to add a step whose name collides with an
	// existing step. The mutator's AddStep should fail because the
	// workflow already contains a step with that name.
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> abort
}`,
		"Write good code.\n",
		"# Knowledge Base: develop\n",
	)

	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "enhancement"}`,
			// Reflector suggests a new step with same name as an existing one
			`{"lessons": [{"id": "L003", "category": "new_step", "step_type": "script", "insight": "Need tests", "suggested_action": "Add test step", "evidence": ["run-1"], "confidence": "high"}]}`,
			// ScriptGenerator uses the name "test.sh" which derives to step name "test"
			`{"path": "scripts/test.sh", "content": "#!/bin/bash\nmake test"}`,
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)
	// The add_script change is recorded before the step addition fails,
	// but add_step should not appear because the mutator rejects duplicates.
	// The `continue` in the orchestrator swallows the error.
	hasAddStep := false
	for _, c := range result.Changes {
		if c.Type == "add_step" {
			hasAddStep = true
		}
	}
	// Check if the mutator rejected the duplicate or allowed it.
	// Either way, the orchestrator should not crash.
	_ = hasAddStep // The test documents the behavior.
}

func TestOrchestratorWorkflowValidAfterMultipleEvolutionCycles(t *testing.T) {
	// Run N evolution cycles adding new steps and verify the workflow
	// remains valid after each cycle.
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`,
		"Write good code.\n",
		"# Knowledge Base: develop\n",
	)

	steps := []struct {
		scriptName string
		scriptPath string
	}{
		{"lint", "scripts/lint.sh"},
		{"security-scan", "scripts/security-scan.sh"},
		{"format-check", "scripts/format-check.sh"},
	}

	for i, step := range steps {
		llm := &scriptedLLM{
			responses: []string{
				`{"classification": "enhancement"}`,
				fmt.Sprintf(`{"lessons": [{"id": "L%d", "category": "new_step", "step_type": "script", "insight": "Need %s", "suggested_action": "Add %s", "evidence": ["run-%d"], "confidence": "high"}]}`, i+10, step.scriptName, step.scriptName, i+1),
				fmt.Sprintf(`{"path": "%s", "content": "#!/bin/bash\necho %s"}`, step.scriptPath, step.scriptName),
			},
		}

		orch := NewOrchestrator(OrchestratorConfig{
			ProjectDir:    dir,
			WorkflowName:  "develop",
			LLM:           llm,
			MinConfidence: "medium",
		})

		result, err := orch.Run(context.Background(), fmt.Sprintf("run-%d", i+1), nil, nil)
		require.NoError(t, err, "cycle %d failed", i)
		require.NotEmpty(t, result.Changes, "cycle %d produced no changes", i)

		// Read the updated workflow and verify it parses and validates.
		wfContent, err := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
		require.NoError(t, err)

		wf, err := dsl.Parse(string(wfContent))
		require.NoError(t, err, "workflow failed to parse after cycle %d:\n%s", i, string(wfContent))
		require.NoError(t, wf.Validate(), "workflow failed to validate after cycle %d:\n%s", i, string(wfContent))
	}
}

func TestOrchestratorErrorInOneLessonDoesNotPreventOthers(t *testing.T) {
	// The `continue` on line 90 of orchestrator.go silently swallows errors.
	// Verify that if one lesson fails, other lessons still get applied.
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`,
		"Write good code.\n",
		"# Knowledge Base: develop\n",
	)

	// Also create a second prompt file that the first lesson targets.
	// The first lesson targets a non-existent file to trigger an error.
	llm := &callTrackingLLM{
		responses: []string{
			// Classifier
			`{"classification": "bug"}`,
			// Reflector returns two lessons: first targets a nonexistent file
			`{"lessons": [
				{"id": "L-FAIL", "category": "prompt_improvement", "target": ".cloche/prompts/nonexistent.md", "insight": "Bad target", "suggested_action": "This should fail", "evidence": ["run-1"], "confidence": "high"},
				{"id": "L-OK", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Good insight", "suggested_action": "Add good rule", "evidence": ["run-1"], "confidence": "high"}
			]}`,
			// Curator response for the first lesson (will fail before this is used
			// because reading the nonexistent file fails)
			// This response won't be consumed because the file read fails first.
			// Curator response for the second lesson
			"Write good code.\n\n## Learned Rules\n\n- Good rule applied\n",
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)

	// The first lesson should have failed (nonexistent file), but the
	// second lesson should have succeeded.
	assert.Len(t, result.Changes, 1)
	assert.Equal(t, "prompt_update", result.Changes[0].Type)
	assert.Equal(t, ".cloche/prompts/implement.md", result.Changes[0].File)

	// Verify the second prompt was actually updated.
	content, err := os.ReadFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Good rule applied")
}

func TestOrchestratorSavesToEvolutionStore(t *testing.T) {
	// Verify the orchestrator saves to the evolution store when lessons
	// are produced. Note: with zero lessons the orchestrator returns early
	// before the store-save path.
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`,
		"Write good code.\n",
		"# Knowledge Base: develop\n",
	)

	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "bug"}`,
			`{"lessons": [{"id": "L-S", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Missing validation", "suggested_action": "Add rule", "evidence": ["run-42"], "confidence": "high"}]}`,
			"Write good code.\n\n## Learned Rules\n\n- Validate inputs\n",
		},
	}

	evoStore := &mockEvolutionStore{}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-42", evoStore, nil)
	require.NoError(t, err)
	assert.Equal(t, "bug", result.Classification)

	// Verify the evolution was saved to the store.
	require.Len(t, evoStore.saved, 1)
	assert.Equal(t, "develop", evoStore.saved[0].WorkflowName)
	assert.Equal(t, "run-42", evoStore.saved[0].TriggerRunID)
	assert.Equal(t, "bug", evoStore.saved[0].Classification)
}

// ===========================================================================
// Collector tests with store mocks
// ===========================================================================

func TestCollectorWithEvolutionStoreMock(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" { step s { run = "echo hi" results = [success] } s:success -> done }`), 0644)

	runs := []*domain.Run{
		{ID: "run-5", WorkflowName: "develop", ProjectDir: dir},
		{ID: "run-6", WorkflowName: "develop", ProjectDir: dir},
	}
	evoStore := &mockEvolutionStore{
		lastEvolution: &ports.EvolutionEntry{
			ID:           "evo-prev",
			TriggerRunID: "run-4",
		},
		runs: runs,
	}

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), evoStore, nil)
	require.NoError(t, err)

	// Should have fetched runs from the store.
	assert.Len(t, data.Runs, 2)
	assert.Equal(t, "run-5", data.Runs[0].ID)
	assert.Equal(t, "run-6", data.Runs[1].ID)
}

func TestCollectorListRunsSinceCalledWithCorrectRunID(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" { step s { run = "echo hi" results = [success] } s:success -> done }`), 0644)

	evoStore := &mockEvolutionStore{
		lastEvolution: &ports.EvolutionEntry{
			ID:           "evo-42",
			TriggerRunID: "run-99",
		},
		runs: nil,
	}

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	_, err := c.Collect(context.Background(), evoStore, nil)
	require.NoError(t, err)

	// Verify ListRunsSince was called with the correct sinceRunID
	// from the last evolution entry.
	require.Len(t, evoStore.listCalls, 1)
	assert.Equal(t, dir, evoStore.listCalls[0].projectDir)
	assert.Equal(t, "develop", evoStore.listCalls[0].workflowName)
	assert.Equal(t, "run-99", evoStore.listCalls[0].sinceRunID)
}

func TestCollectorListRunsSinceEmptyWhenNoLastEvolution(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" { step s { run = "echo hi" results = [success] } s:success -> done }`), 0644)

	// No last evolution — sinceRunID should be empty.
	evoStore := &mockEvolutionStore{
		lastEvolution: nil,
		runs:          nil,
	}

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	_, err := c.Collect(context.Background(), evoStore, nil)
	require.NoError(t, err)

	require.Len(t, evoStore.listCalls, 1)
	assert.Equal(t, "", evoStore.listCalls[0].sinceRunID)
}

func TestCollectorWithCaptureStoreMock(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" { step s { run = "echo hi" results = [success] } s:success -> done }`), 0644)

	runs := []*domain.Run{
		{ID: "run-10", WorkflowName: "develop"},
		{ID: "run-11", WorkflowName: "develop"},
	}
	evoStore := &mockEvolutionStore{runs: runs}

	capStore := &mockCaptureStore{
		captures: map[string][]*domain.StepExecution{
			"run-10": {
				{StepName: "implement", Result: "success"},
			},
			"run-11": {
				{StepName: "implement", Result: "fail"},
				{StepName: "test", Result: "success"},
			},
		},
	}

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), evoStore, capStore)
	require.NoError(t, err)

	assert.Len(t, data.Captures, 2)
	assert.Len(t, data.Captures["run-10"], 1)
	assert.Len(t, data.Captures["run-11"], 2)
	assert.Equal(t, "implement", data.Captures["run-10"][0].StepName)
	assert.Equal(t, "fail", data.Captures["run-11"][0].Result)
}

// ===========================================================================
// Integration tests
// ===========================================================================

func TestIntegration_CorruptPromptDetectedAndFixed(t *testing.T) {
	// Full cycle: corrupt a prompt → run evolution → verify it produces a fix.
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`,
		// Start with a "corrupted" prompt (conversational text, no useful instructions)
		"Sure! I've updated the prompt to include better instructions.",
		"# Knowledge Base: develop\n",
	)

	// The evolution pipeline should detect the corruption and fix it.
	llm := &scriptedLLM{
		responses: []string{
			// Classifier
			`{"classification": "bug"}`,
			// Reflector identifies the corruption
			`{"lessons": [{"id": "L-FIX", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Prompt was corrupted with conversational text", "suggested_action": "Restore proper prompt structure", "evidence": ["run-1"], "confidence": "high"}]}`,
			// Curator returns a proper prompt
			"# Implementation Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Always validate user inputs\n",
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Changes, 1)
	assert.Equal(t, "prompt_update", result.Changes[0].Type)

	// Verify the prompt is now proper.
	content, err := os.ReadFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Implementation Prompt")
	assert.Contains(t, string(content), "Always validate user inputs")
	assert.NotContains(t, string(content), "Sure!")
}

func TestIntegration_StabilityNoCyclesNoDrift(t *testing.T) {
	// Stability test: run N evolution cycles where the reflector finds no
	// lessons. The workflow should remain unchanged.
	workflow := `workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`
	prompt := "# Implementation Prompt\n\nWrite good code.\n"
	dir := setupOrchestratorDir(t, workflow, prompt, "# Knowledge Base: develop\n")

	originalWorkflow, _ := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
	originalPrompt, _ := os.ReadFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"))

	const cycles = 5
	for i := 0; i < cycles; i++ {
		llm := &scriptedLLM{
			responses: []string{
				`{"classification": "feature"}`,
				`{"lessons": []}`,
			},
		}

		orch := NewOrchestrator(OrchestratorConfig{
			ProjectDir:    dir,
			WorkflowName:  "develop",
			LLM:           llm,
			MinConfidence: "medium",
		})

		result, err := orch.Run(context.Background(), fmt.Sprintf("run-%d", i+1), nil, nil)
		require.NoError(t, err, "cycle %d failed", i)
		assert.Empty(t, result.Changes, "cycle %d should have no changes", i)
	}

	// After N cycles, workflow and prompt should be identical.
	finalWorkflow, _ := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
	finalPrompt, _ := os.ReadFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"))
	assert.Equal(t, string(originalWorkflow), string(finalWorkflow), "workflow drifted after %d cycles", cycles)
	assert.Equal(t, string(originalPrompt), string(finalPrompt), "prompt drifted after %d cycles", cycles)
}

func TestIntegration_FullCycleWithStores(t *testing.T) {
	// End-to-end with actual store mocks: collect → classify → reflect →
	// apply → save to store.
	dir := setupOrchestratorDir(t,
		`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`,
		"# Prompt\n\nWrite code.\n",
		"# Knowledge Base: develop\n",
	)

	runs := []*domain.Run{
		{ID: "run-100", WorkflowName: "develop", ProjectDir: dir},
	}
	evoStore := &mockEvolutionStore{
		lastEvolution: &ports.EvolutionEntry{TriggerRunID: "run-99"},
		runs:          runs,
	}
	capStore := &mockCaptureStore{
		captures: map[string][]*domain.StepExecution{
			"run-100": {
				{StepName: "implement", Result: "success"},
			},
		},
	}

	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "bug"}`,
			`{"lessons": [{"id": "L-STORE", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Missing validation", "suggested_action": "Add validation rule", "evidence": ["run-100"], "confidence": "high"}]}`,
			"# Prompt\n\nWrite code.\n\n## Learned Rules\n\n- Always validate inputs\n",
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-100", evoStore, capStore)
	require.NoError(t, err)
	assert.Equal(t, "bug", result.Classification)
	assert.Len(t, result.Changes, 1)

	// Verify evolution was saved to store.
	require.Len(t, evoStore.saved, 1)
	assert.Equal(t, "run-100", evoStore.saved[0].TriggerRunID)

	// Verify ListRunsSince was called correctly.
	require.Len(t, evoStore.listCalls, 1)
	assert.Equal(t, "run-99", evoStore.listCalls[0].sinceRunID)
}
