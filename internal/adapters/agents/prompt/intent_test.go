package prompt_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mapKV is a minimal in-memory prompt.KVReader stub for exercising
// $intent/auto-prepend resolution without a real store.
type mapKV map[string]string

func (m mapKV) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}

const intentBlock = "## Standing project requirements\n\n- [req-a1b2] never do X\n"

func captureStdin(dir string) *prompt.Adapter {
	return &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}
}

func readCaptured(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	return string(data)
}

func TestPromptAdapter_AutoPrependsIntentBlockFromKV(t *testing.T) {
	dir := t.TempDir()
	adapter := captureStdin(dir)
	adapter.KV = mapKV{"intent": intentBlock}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Implement the feature."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured := readCaptured(t, dir)
	assert.Contains(t, captured, "Standing project requirements")
	assert.Less(t, strings.Index(captured, "Standing project requirements"), strings.Index(captured, "Implement the feature."))
}

func TestPromptAdapter_ExplicitIntentPlacementSuppressesAutoPrepend(t *testing.T) {
	dir := t.TempDir()
	adapter := captureStdin(dir)
	adapter.KV = mapKV{"intent": intentBlock}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Context:\n{{ $intent }}\n\nImplement the feature."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured := readCaptured(t, dir)
	assert.Equal(t, 1, strings.Count(captured, "Standing project requirements"))
}

func TestPromptAdapter_NoIntentKV_ByteIdenticalPrompt(t *testing.T) {
	dir := t.TempDir()
	adapter := captureStdin(dir)
	adapter.KV = mapKV{} // no "intent" key — dormant project

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Implement the feature."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured := readCaptured(t, dir)
	assert.NotContains(t, captured, "Standing project requirements")
}

func TestPromptAdapter_NilKV_NoPrepend(t *testing.T) {
	dir := t.TempDir()
	adapter := captureStdin(dir) // KV left nil

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Implement the feature."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured := readCaptured(t, dir)
	assert.NotContains(t, captured, "Standing project requirements")
}

func TestPromptAdapter_SimilarVariableName_DoesNotSuppressAutoPrepend(t *testing.T) {
	dir := t.TempDir()
	adapter := captureStdin(dir)
	adapter.KV = mapKV{"intent": intentBlock}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		// "$intentionally" is a different identifier (and not inside a {{ }}
		// directive, so it isn't resolved at all) and must not be mistaken
		// for an explicit {{ $intent }} placement that would suppress prepend.
		Config: map[string]string{"prompt": "Do this $intentionally.\n\nImplement the feature."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured := readCaptured(t, dir)
	assert.Contains(t, captured, "Standing project requirements")
	assert.Contains(t, captured, "$intentionally")
}

func TestPromptAdapter_EmptyIntentBlock_NoPrepend(t *testing.T) {
	dir := t.TempDir()
	adapter := captureStdin(dir)
	adapter.KV = mapKV{"intent": ""} // active project, nothing selected for this step

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Implement the feature."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured := readCaptured(t, dir)
	assert.NotContains(t, captured, "Standing project requirements")
}
