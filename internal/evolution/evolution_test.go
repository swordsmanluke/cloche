package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/dsl"
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
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.jsonl"),
		[]byte(`{"id":"L000","insight":"existing lesson"}`+"\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"),
		[]byte("Write good code"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step impl {
    prompt = file(".cloche/prompts/implement.md")
    results = [success]
  }
  impl:success -> done
}`), 0644)

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), nil, nil)
	require.NoError(t, err)

	assert.Contains(t, data.KnowledgeBase, "existing lesson")
	assert.Contains(t, data.CurrentPrompts, ".cloche/prompts/implement.md")
	assert.Equal(t, "Write good code", data.CurrentPrompts[".cloche/prompts/implement.md"])
	assert.NotEmpty(t, data.CurrentWorkflow)
	assert.Equal(t, dir, data.ProjectDir)
	assert.Equal(t, "develop", data.WorkflowName)
}

func TestCollectorNoKnowledgeBase(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" { step s { run = "echo hi" results = [success] } s:success -> done }`), 0644)

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, data.KnowledgeBase)
}

func TestExtractPromptFiles(t *testing.T) {
	workflow := `step impl { prompt = file(".cloche/prompts/implement.md") }
step fix { prompt = file(".cloche/prompts/fix.md") }
step test { run = "make test" }`

	files := extractPromptFiles(workflow)
	assert.Len(t, files, 2)
	assert.Contains(t, files, ".cloche/prompts/implement.md")
	assert.Contains(t, files, ".cloche/prompts/fix.md")
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
				"target":           ".cloche/prompts/implement.md",
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
		Changes:      []Change{{Type: "prompt_update", File: ".cloche/prompts/impl.md"}},
	}
	require.NoError(t, logger.Log(result1))

	result2 := &EvolutionResult{
		ID:           "evo-2",
		WorkflowName: "develop",
		Changes:      []Change{{Type: "add_step", File: ".cloche/develop.cloche"}},
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
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"), []byte("original content"), 0644)

	logger := &AuditLogger{ProjectDir: dir}
	snapName, err := logger.Snapshot(".cloche/prompts/implement.md")
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

	content, err := os.ReadFile(filepath.Join(kbDir, "develop.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "P001")
	assert.Contains(t, string(content), "sanitize HTML")
	assert.Contains(t, string(content), "run-1")
}

func TestAuditLoggerDeduplicatesLessons(t *testing.T) {
	dir := t.TempDir()
	logger := &AuditLogger{ProjectDir: dir}

	lesson := Lesson{
		ID:       "L001",
		Category: "prompt_improvement",
		Insight:  "Original insight",
	}

	require.NoError(t, logger.UpdateKnowledge("develop", []Lesson{lesson}))
	require.NoError(t, logger.UpdateKnowledge("develop", []Lesson{lesson}))

	// Read back and verify only one entry
	lessons, err := readKnowledge(logger.KnowledgePath("develop"))
	require.NoError(t, err)
	assert.Len(t, lessons, 1)
	assert.Equal(t, "L001", lessons[0].ID)
}

func TestAuditLoggerUpdatesExistingLesson(t *testing.T) {
	dir := t.TempDir()
	logger := &AuditLogger{ProjectDir: dir}

	original := Lesson{ID: "L001", Insight: "Original insight"}
	updated := Lesson{ID: "L001", Insight: "Updated insight"}

	require.NoError(t, logger.UpdateKnowledge("develop", []Lesson{original}))
	require.NoError(t, logger.UpdateKnowledge("develop", []Lesson{updated}))

	lessons, err := readKnowledge(logger.KnowledgePath("develop"))
	require.NoError(t, err)
	assert.Len(t, lessons, 1)
	assert.Equal(t, "Updated insight", lessons[0].Insight)
}

func TestAuditLoggerPrunesWithMaxPromptBullets(t *testing.T) {
	dir := t.TempDir()
	logger := &AuditLogger{ProjectDir: dir, MaxPromptBullets: 5}

	// Add 10 lessons
	for i := 0; i < 10; i++ {
		lesson := Lesson{
			ID:      fmt.Sprintf("L%03d", i),
			Insight: fmt.Sprintf("Insight %d", i),
		}
		require.NoError(t, logger.UpdateKnowledge("develop", []Lesson{lesson}))
	}

	lessons, err := readKnowledge(logger.KnowledgePath("develop"))
	require.NoError(t, err)
	assert.Len(t, lessons, 5)
	// Should keep the most recent 5 (L005-L009)
	assert.Equal(t, "L005", lessons[0].ID)
	assert.Equal(t, "L009", lessons[4].ID)
}

// --- LLM Client tests ---

// --- Curator tests ---

func TestCuratorUpdatesPrompt(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.WriteFile(promptPath, []byte("# Implementation Prompt\n\nWrite good code.\n"), 0644)

	updatedContent := "# Implementation Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Always sanitize user inputs\n"
	llm := &fakeLLM{response: updatedContent}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		ID:              "lesson-001",
		Category:        "prompt_improvement",
		Target:          ".cloche/prompts/implement.md",
		Insight:         "XSS vulnerabilities in form handlers",
		SuggestedAction: "Add sanitization rule",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "prompt_update", change.Type)
	assert.Equal(t, ".cloche/prompts/implement.md", change.File)
	assert.NotEmpty(t, change.Snapshot)

	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "sanitize user inputs")
}

// --- stripCodeFences tests ---

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no fences returns as-is",
			input:    "# Prompt\n\nDo good work.\n",
			expected: "# Prompt\n\nDo good work.",
		},
		{
			name:     "plain code fence",
			input:    "```\n# Prompt\n\nDo good work.\n```",
			expected: "# Prompt\n\nDo good work.\n",
		},
		{
			name:     "code fence with language hint",
			input:    "```markdown\n# Prompt\n\nDo good work.\n```",
			expected: "# Prompt\n\nDo good work.\n",
		},
		{
			name:     "commentary before and after fence",
			input:    "Here's the updated prompt:\n\n```markdown\n# Prompt\n\nDo good work.\n```\n\nI've added the rule as requested.",
			expected: "# Prompt\n\nDo good work.\n",
		},
		{
			name:     "no closing fence returns as-is",
			input:    "```markdown\n# Prompt\n",
			expected: "```markdown\n# Prompt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, stripCodeFences(tt.input))
		})
	}
}

func TestCuratorStripsCodeFencesFromResponse(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.WriteFile(promptPath, []byte("# Implementation Prompt\n\nWrite good code.\n"), 0644)

	// LLM returns content wrapped in code fences with commentary
	fencedResponse := "Here is the updated prompt:\n\n```markdown\n# Implementation Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Always validate inputs\n```\n\nI added the validation rule."
	llm := &fakeLLM{response: fencedResponse}

	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Missing input validation",
		SuggestedAction: "Add validation rule",
	}

	_, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)

	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)

	// Should contain the actual prompt content, not the commentary
	assert.Contains(t, string(content), "# Implementation Prompt")
	assert.Contains(t, string(content), "Always validate inputs")
	assert.NotContains(t, string(content), "Here is the updated prompt")
	assert.NotContains(t, string(content), "I added the validation rule")
	assert.NotContains(t, string(content), "```")
}

func TestCuratorRejectsConversationalMetaText(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "fix.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	original := "# Fix Prompt\n\nFix bugs carefully.\n"
	os.WriteFile(promptPath, []byte(original), 0644)

	// LLM returns conversational meta-text
	llm := &fakeLLM{response: "I need write permission to update the file. Could you grant access to .cloche/prompts/fix.md?"}
	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/fix.md",
		Insight:         "Missing error context",
		SuggestedAction: "Wrap errors with context",
	}

	_, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)

	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	// Must NOT contain the conversational text
	assert.NotContains(t, string(content), "I need write permission")
	assert.NotContains(t, string(content), "Could you grant access")
	// Should contain the original content and the lesson appended directly
	assert.Contains(t, string(content), "# Fix Prompt")
	assert.Contains(t, string(content), "Missing error context")
}

func TestCuratorRejectsUnfencedConversationalPrefix(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	original := "# Implementation Prompt\n\nWrite good code.\n"
	os.WriteFile(promptPath, []byte(original), 0644)

	// LLM returns "Here is the updated prompt:" with NO code fences
	llm := &fakeLLM{response: "Here is the updated prompt:\n\n# Implementation Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Validate inputs\n"}
	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Missing validation",
		SuggestedAction: "Add input validation",
	}

	_, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)

	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	// The conversational prefix must not appear
	assert.NotContains(t, string(content), "Here is the updated prompt")
	// Original content preserved, lesson appended via fallback
	assert.Contains(t, string(content), "# Implementation Prompt")
	assert.Contains(t, string(content), "Missing validation")
}

func TestCuratorAcceptsValidPromptWithoutFences(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.WriteFile(promptPath, []byte("# Implementation Prompt\n\nWrite good code.\n"), 0644)

	// LLM returns valid prompt content without code fences
	validResponse := "# Implementation Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Always validate inputs\n"
	llm := &fakeLLM{response: validResponse}
	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Missing validation",
		SuggestedAction: "Add validation rule",
	}

	_, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)

	content, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	// Should write the LLM response (after stripCodeFences trimming) since it's valid prompt content
	assert.Contains(t, string(content), "Always validate inputs")
	assert.Contains(t, string(content), "# Implementation Prompt")
	assert.NotContains(t, string(content), "Missing validation")
}

func TestIsConversationalResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"permission request", "I need write permission to update the file", true},
		{"grant request", "Could you grant access to the file?", true},
		{"blocked message", "Permission blocked by sandbox", true},
		{"here is updated", "Here is the updated prompt:\n\n# Prompt", true},
		{"heres updated", "Here's the updated prompt:\n\n# Prompt", true},
		{"ive updated", "I've updated the prompt with the new rule", true},
		{"let me", "Let me update the file for you", true},
		{"valid markdown heading", "# Prompt\n\nDo good work.", false},
		{"valid bullet list", "- Rule one\n- Rule two", false},
		{"empty string", "", true},
		{"valid content no heading", "Write good code and test everything.", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isConversationalResponse(tt.input))
		})
	}
}

func TestAppendLessonDirectly(t *testing.T) {
	t.Run("creates learned rules section", func(t *testing.T) {
		result := appendLessonDirectly("# Prompt\n\nDo good work.\n", &Lesson{
			Insight:         "Missing validation",
			SuggestedAction: "Add validation",
		})
		assert.Contains(t, result, "## Learned Rules")
		assert.Contains(t, result, "Missing validation: Add validation")
	})

	t.Run("appends to existing learned rules", func(t *testing.T) {
		existing := "# Prompt\n\n## Learned Rules\n\n- Existing rule\n"
		result := appendLessonDirectly(existing, &Lesson{
			Insight:         "New insight",
			SuggestedAction: "New action",
		})
		assert.Contains(t, result, "- Existing rule")
		assert.Contains(t, result, "- New insight: New action")
		// Should not create a duplicate section
		assert.Equal(t, 1, strings.Count(result, "## Learned Rules"))
	})
}

// --- Script Generator tests ---

func TestScriptGeneratorCreatesScript(t *testing.T) {
	dir := t.TempDir()

	llm := &fakeLLM{response: `{"path": "scripts/security-scan.sh", "content": "#!/bin/bash\ngosec ./..."}`}
	g := &ScriptGenerator{LLM: llm}

	lesson := &Lesson{
		ID:              "lesson-001",
		Category:        "new_step",
		StepType:        "script",
		Insight:         "No security scanning",
		SuggestedAction: "Add gosec security scan",
	}

	result, err := g.Generate(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "scripts/security-scan.sh", result.Path)

	content, err := os.ReadFile(filepath.Join(dir, "scripts", "security-scan.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "gosec")

	info, err := os.Stat(filepath.Join(dir, "scripts", "security-scan.sh"))
	require.NoError(t, err)
	assert.Zero(t, info.Mode()&0111) // non-executable; workflow engine handles execution
}

// --- LLM Client tests ---

func TestCommandLLMClientPrintMode(t *testing.T) {
	// Use a shell script that dumps its args and stdin so we can verify
	// that the client passes the right flags and prompt separation.
	script := filepath.Join(t.TempDir(), "fake-claude.sh")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "ARGS:$*"
echo "STDIN:$(cat)"
`), 0755)

	c := &CommandLLMClient{Command: script, Args: []string{}}
	result, err := c.Complete(context.Background(), "You are a helpful assistant.", "Summarize this text")
	require.NoError(t, err)

	// Should include -p flag for print/non-interactive mode
	assert.Contains(t, result, "-p")

	// Should pass system prompt via --system-prompt flag
	assert.Contains(t, result, "--system-prompt")
	assert.Contains(t, result, "You are a helpful assistant.")

	// Should pass --output-format text
	assert.Contains(t, result, "--output-format text")

	// Stdin should contain only the user prompt, not the system prompt
	assert.Contains(t, result, "STDIN:Summarize this text")
	assert.NotContains(t, result, "STDIN:You are a helpful assistant")
}

func TestCommandLLMClientEmptySystemPrompt(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-claude.sh")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "ARGS:$*"
echo "STDIN:$(cat)"
`), 0755)

	c := &CommandLLMClient{Command: script, Args: []string{}}
	result, err := c.Complete(context.Background(), "", "just user prompt")
	require.NoError(t, err)

	// Should NOT include --system-prompt when system prompt is empty
	assert.NotContains(t, result, "--system-prompt")
	// Should still have -p and --output-format
	assert.Contains(t, result, "-p")
	assert.Contains(t, result, "--output-format text")
	assert.Contains(t, result, "STDIN:just user prompt")
}

func TestCommandLLMClientExtraArgs(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-claude.sh")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "ARGS:$*"
echo "STDIN:$(cat)"
`), 0755)

	c := &CommandLLMClient{Command: script, Args: []string{"--model", "sonnet"}}
	result, err := c.Complete(context.Background(), "sys", "usr")
	require.NoError(t, err)

	// Extra args should appear before the appended flags
	assert.Contains(t, result, "--model sonnet")
	assert.Contains(t, result, "-p")
}

func TestCommandLLMClientDoesNotMutateArgs(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-claude.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ncat\n"), 0755)

	original := []string{"--verbose"}
	c := &CommandLLMClient{Command: script, Args: original}
	_, err := c.Complete(context.Background(), "sys", "usr")
	require.NoError(t, err)

	// The original Args slice should not be modified between calls.
	assert.Equal(t, []string{"--verbose"}, original)
}

// --- Orchestrator tests ---

// scriptedLLM returns responses in order.
type scriptedLLM struct {
	responses []string
	idx       int
}

func (s *scriptedLLM) Complete(ctx context.Context, system, user string) (string, error) {
	resp := s.responses[s.idx]
	s.idx++
	return resp, nil
}

func TestOrchestratorEndToEnd(t *testing.T) {
	dir := t.TempDir()

	// Set up project structure
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"),
		[]byte("Write good code.\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  implement:success -> test
  test:success -> done
  test:fail -> abort
}`), 0644)

	// Fake LLM that returns appropriate responses per stage
	llm := &scriptedLLM{
		responses: []string{
			// Classifier
			`{"classification": "bug"}`,
			// Reflector
			`{"lessons": [{"id": "L001", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "XSS pattern", "suggested_action": "Add sanitization rule", "evidence": ["run-1"], "confidence": "high"}]}`,
			// Curator
			"Write good code.\n\n## Learned Rules\n\n- Always sanitize user inputs\n",
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
	assert.Equal(t, "bug", result.Classification)
	require.Len(t, result.Changes, 1)
	assert.Equal(t, "prompt_update", result.Changes[0].Type)

	// Verify prompt was actually updated
	content, _ := os.ReadFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"))
	assert.Contains(t, string(content), "sanitize user inputs")

	// Verify audit log was written
	logContent, _ := os.ReadFile(filepath.Join(dir, ".cloche", "evolution", "log.jsonl"))
	assert.Contains(t, string(logContent), "prompt_update")
	assert.Contains(t, string(logContent), "XSS pattern")

	// Verify knowledge base was updated (JSONL format)
	kbContent, _ := os.ReadFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.jsonl"))
	assert.Contains(t, string(kbContent), "L001")
}

func TestOrchestratorNewStepWiredIntoGraph(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	// Knowledge base uses JSONL format (created automatically by UpdateKnowledge)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
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
}`), 0644)

	llm := &scriptedLLM{
		responses: []string{
			// Classifier
			`{"classification": "enhancement"}`,
			// Reflector
			`{"lessons": [{"id": "L002", "category": "new_step", "step_type": "script", "insight": "No security scanning", "suggested_action": "Add gosec scan", "evidence": ["run-2"], "confidence": "high"}]}`,
			// ScriptGenerator
			`{"path": "scripts/security-scan.sh", "content": "#!/bin/bash\ngosec ./..."}`,
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-2", nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Changes, 2) // add_script + add_step
	assert.Equal(t, "add_script", result.Changes[0].Type)
	assert.Equal(t, "add_step", result.Changes[1].Type)

	// Read the updated workflow and verify the new step is reachable
	wfContent, err := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
	require.NoError(t, err)

	wfStr := string(wfContent)
	// The new step should be wired into the graph
	assert.Contains(t, wfStr, "step security-scan")
	// An existing step should wire to the new step (rewired from -> done)
	assert.Contains(t, wfStr, "test:success -> security-scan")
	// The new step should wire its results to terminals
	assert.Contains(t, wfStr, "security-scan:success -> done")
	assert.Contains(t, wfStr, "security-scan:fail -> abort")

	// Verify the workflow parses and validates
	wf, err := dsl.Parse(wfStr)
	require.NoError(t, err)
	require.NoError(t, wf.Validate())
}

func TestHandleNewStepSkipsDuplicate(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)

	// Workflow already contains a step named "security-scan"
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  step security-scan {
    run = "scripts/security-scan.sh"
    results = [success, fail]
  }
  implement:success -> security-scan
  implement:fail -> abort
  security-scan:success -> done
  security-scan:fail -> abort
}`), 0644)

	llm := &scriptedLLM{
		responses: []string{
			// Classifier
			`{"classification": "enhancement"}`,
			// Reflector — lesson wants to add "security-scan" again
			`{"lessons": [{"id": "L003", "category": "new_step", "step_type": "script", "insight": "Add security scanning", "suggested_action": "Add gosec scan", "evidence": ["run-3"], "confidence": "high"}]}`,
			// ScriptGenerator
			`{"path": "scripts/security-scan.sh", "content": "#!/bin/bash\ngosec ./..."}`,
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

	// Should have add_script change but NOT add_step (skipped as duplicate)
	stepChanges := 0
	for _, c := range result.Changes {
		if c.Type == "add_step" {
			stepChanges++
		}
	}
	assert.Equal(t, 0, stepChanges, "should not add a duplicate step")

	// Workflow should be unchanged — still valid, no duplicates
	wfContent, err := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
	require.NoError(t, err)
	wf, err := dsl.Parse(string(wfContent))
	require.NoError(t, err)
	require.NoError(t, wf.Validate())
	assert.Equal(t, 2, len(wf.Steps), "should still have exactly 2 steps")
}

func TestHandleNewStepNoDoneEdge(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)

	// Workflow has no -> done edges (all go to abort)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`), 0644)

	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "enhancement"}`,
			`{"lessons": [{"id": "L004", "category": "new_step", "step_type": "script", "insight": "Add linting", "suggested_action": "Add lint step", "evidence": ["run-4"], "confidence": "high"}]}`,
			`{"path": "scripts/lint.sh", "content": "#!/bin/bash\ngolangci-lint run"}`,
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-4", nil, nil)
	require.NoError(t, err)

	// The new step should be added and the resulting workflow must validate
	wfContent, err := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
	require.NoError(t, err)
	wf, err := dsl.Parse(string(wfContent))
	require.NoError(t, err)
	require.NoError(t, wf.Validate(), "resulting workflow should have no orphaned steps")

	// The new step should be reachable
	_, hasStep := wf.Steps["lint"]
	assert.True(t, hasStep, "lint step should exist")

	// Verify add_step change was recorded
	hasAddStep := false
	for _, c := range result.Changes {
		if c.Type == "add_step" {
			hasAddStep = true
		}
	}
	assert.True(t, hasAddStep, "should have recorded add_step change")
}

func TestHandleNewStepValidatesResultingWorkflow(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
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
}`), 0644)

	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "enhancement"}`,
			`{"lessons": [{"id": "L005", "category": "new_step", "step_type": "script", "insight": "Add formatting check", "suggested_action": "Add gofmt check", "evidence": ["run-5"], "confidence": "high"}]}`,
			`{"path": "scripts/format-check.sh", "content": "#!/bin/bash\ngofmt -l ."}`,
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-5", nil, nil)
	require.NoError(t, err)

	// Read and fully validate the workflow
	wfContent, err := os.ReadFile(filepath.Join(dir, ".cloche", "develop.cloche"))
	require.NoError(t, err)
	wf, err := dsl.Parse(string(wfContent))
	require.NoError(t, err)
	require.NoError(t, wf.Validate())

	// Verify we got 3 steps now
	assert.Equal(t, 3, len(wf.Steps))

	// Verify we got the add_step change
	hasAddStep := false
	for _, c := range result.Changes {
		if c.Type == "add_step" {
			hasAddStep = true
		}
	}
	assert.True(t, hasAddStep)
}

func TestOrchestratorNoLessons(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step s {
    run = "echo hi"
    results = [success]
  }
  s:success -> done
}`), 0644)

	llm := &scriptedLLM{
		responses: []string{
			// Classifier
			`{"classification": "feature"}`,
			// Reflector — no lessons
			`{"lessons": []}`,
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
	assert.Equal(t, "feature", result.Classification)
	assert.Empty(t, result.Changes)
}

// --- Content-change guard tests ---

func TestLessonAlreadyPresent(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		lesson  *Lesson
		present bool
	}{
		{
			name:    "insight present",
			prompt:  "# Prompt\n\n## Learned Rules\n\n- Always sanitize user inputs\n",
			lesson:  &Lesson{Insight: "Always sanitize user inputs", SuggestedAction: "Add rule"},
			present: true,
		},
		{
			name:    "action present",
			prompt:  "# Prompt\n\n## Learned Rules\n\n- Add input validation for all forms\n",
			lesson:  &Lesson{Insight: "Forms lack validation", SuggestedAction: "Add input validation for all forms"},
			present: true,
		},
		{
			name:    "case insensitive match",
			prompt:  "# Prompt\n\n## Learned Rules\n\n- always sanitize user inputs\n",
			lesson:  &Lesson{Insight: "Always Sanitize User Inputs", SuggestedAction: "Do it"},
			present: true,
		},
		{
			name:    "not present",
			prompt:  "# Prompt\n\nWrite good code.\n",
			lesson:  &Lesson{Insight: "Always sanitize user inputs", SuggestedAction: "Add sanitization"},
			present: false,
		},
		{
			name:    "empty insight and action",
			prompt:  "# Prompt\n\nWrite good code.\n",
			lesson:  &Lesson{Insight: "", SuggestedAction: ""},
			present: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.present, lessonAlreadyPresent(tt.prompt, tt.lesson))
		})
	}
}

func TestCuratorSkipsWhenLessonAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)

	// Prompt already contains the lesson insight
	os.WriteFile(promptPath, []byte("# Prompt\n\n## Learned Rules\n\n- Always sanitize user inputs\n"), 0644)

	// LLM should NOT be called — use a response that would change the file
	llm := &fakeLLM{response: "THIS SHOULD NOT APPEAR"}
	audit := &AuditLogger{ProjectDir: dir}
	c := &Curator{LLM: llm, Audit: audit}
	lesson := &Lesson{
		Target:          ".cloche/prompts/implement.md",
		Insight:         "Always sanitize user inputs",
		SuggestedAction: "Add sanitization rule",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Nil(t, change, "should return nil change when lesson already present")

	// File should be unchanged
	content, _ := os.ReadFile(promptPath)
	assert.NotContains(t, string(content), "THIS SHOULD NOT APPEAR")
}

func TestReflectorFiltersAlreadyAppliedLessons(t *testing.T) {
	lessonsJSON, _ := json.Marshal(map[string]any{
		"lessons": []map[string]any{
			{"id": "lesson-001", "category": "prompt_improvement", "confidence": "high"},
			{"id": "lesson-002", "category": "prompt_improvement", "confidence": "high"},
		},
	})

	llm := &fakeLLM{response: string(lessonsJSON)}
	r := &Reflector{LLM: llm, MinConfidence: "low"}

	// Knowledge base already contains lesson-001
	data := &CollectedData{
		KnowledgeBase: "# Knowledge Base\n\n- **[lesson-001]** (prompt_improvement, confidence: high) Old insight\n",
	}
	lessons, err := r.Reflect(context.Background(), data, "bug")
	require.NoError(t, err)
	require.Len(t, lessons, 1)
	assert.Equal(t, "lesson-002", lessons[0].ID)
}

func TestOrchestratorSkipsAlreadyAppliedLessons(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.jsonl"),
		[]byte(`{"id":"L001","category":"prompt_improvement","insight":"XSS pattern","suggested_action":"Add sanitization","confidence":"high"}`+"\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"),
		[]byte("Write good code.\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step s {
    run = "echo hi"
    results = [success]
  }
  s:success -> done
}`), 0644)

	// Reflector returns a lesson that's already in the knowledge base
	llm := &scriptedLLM{
		responses: []string{
			`{"classification": "bug"}`,
			`{"lessons": [{"id": "L001", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "XSS pattern", "suggested_action": "Add sanitization", "evidence": ["run-1"], "confidence": "high"}]}`,
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "low",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)
	assert.Empty(t, result.Changes, "should produce zero changes for already-applied lesson")
}

func TestOrchestratorSecondRunProducesNoChanges(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md"),
		[]byte("# Knowledge Base: develop\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"),
		[]byte("Write good code.\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"),
		[]byte(`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  implement:success -> done
  implement:fail -> abort
}`), 0644)

	// First run: lesson gets applied
	llm1 := &scriptedLLM{
		responses: []string{
			`{"classification": "bug"}`,
			`{"lessons": [{"id": "L010", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Always sanitize user inputs", "suggested_action": "Add sanitization rule", "evidence": ["run-1"], "confidence": "high"}]}`,
			"Write good code.\n\n## Learned Rules\n\n- Always sanitize user inputs\n",
		},
	}

	orch1 := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm1,
		MinConfidence: "low",
	})

	result1, err := orch1.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)
	require.Len(t, result1.Changes, 1, "first run should produce one change")

	// Second run: same lesson ID — the knowledge base now contains L010,
	// and the prompt contains the insight text.
	llm2 := &scriptedLLM{
		responses: []string{
			`{"classification": "bug"}`,
			`{"lessons": [{"id": "L010", "category": "prompt_improvement", "target": ".cloche/prompts/implement.md", "insight": "Always sanitize user inputs", "suggested_action": "Add sanitization rule", "evidence": ["run-1"], "confidence": "high"}]}`,
		},
	}

	orch2 := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm2,
		MinConfidence: "low",
	})

	result2, err := orch2.Run(context.Background(), "run-2", nil, nil)
	require.NoError(t, err)
	assert.Empty(t, result2.Changes, "second run should produce zero changes")
}
