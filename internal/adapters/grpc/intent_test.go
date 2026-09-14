package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIntentFixtureProjectForGRPC(t *testing.T) (dir, reqID string) {
	t.Helper()
	dir = t.TempDir()
	store := intent.NewStore(dir)
	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Never bump the major version unless explicitly told to.",
	})
	require.NoError(t, err)
	return dir, created.ID
}

func TestDaemonExecutor_SeedIntentKV_WritesBlockAndIDs(t *testing.T) {
	tmpDir, reqID := newIntentFixtureProjectForGRPC(t)

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	wf := buildContainerWFForTest("develop")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task1",
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	de.seedIntentKV(context.Background(), step, wf, "run1")

	block, ok := store.ctxKeys["intent"]
	require.True(t, ok, "intent block should be seeded")
	assert.Contains(t, block, reqID)
	assert.Contains(t, block, "Standing project requirements")

	ids, ok := store.ctxKeys["develop:step1:intent"]
	require.True(t, ok, "injected IDs should be recorded per step")
	assert.Equal(t, reqID, ids)
}

func TestDaemonExecutor_SeedIntentKV_NoIntentDir_NoKVWrites(t *testing.T) {
	tmpDir := t.TempDir()

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	wf := buildContainerWFForTest("develop")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task1",
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	de.seedIntentKV(context.Background(), step, wf, "run1")

	assert.Empty(t, store.ctxKeys, "no intent dir should mean no KV seeding at all")
}

func TestDaemonExecutor_SeedIntentKV_StepLevelOff_SkipsSeeding(t *testing.T) {
	tmpDir, _ := newIntentFixtureProjectForGRPC(t)

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	wf := buildContainerWFForTest("develop")
	wf.Steps["step1"].Config["intent"] = "off"

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task1",
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	de.seedIntentKV(context.Background(), wf.Steps["step1"], wf, "run1")

	assert.Empty(t, store.ctxKeys)
}

func TestDaemonExecutor_SeedIntentKV_WorkflowLevelOff_SkipsSeeding(t *testing.T) {
	tmpDir, _ := newIntentFixtureProjectForGRPC(t)

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	wf := buildContainerWFForTest("develop")
	wf.Config = map[string]string{"intent": "off"}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task1",
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	de.seedIntentKV(context.Background(), wf.Steps["step1"], wf, "run1")

	assert.Empty(t, store.ctxKeys)
}

func TestDaemonExecutor_SeedIntentKV_ConfigTomlInjectOff_SkipsSeeding(t *testing.T) {
	tmpDir, _ := newIntentFixtureProjectForGRPC(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".cloche", "config.toml"), []byte("[intent]\ninject = \"off\"\n"), 0644))

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	wf := buildContainerWFForTest("develop")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task1",
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	de.seedIntentKV(context.Background(), wf.Steps["step1"], wf, "run1")

	assert.Empty(t, store.ctxKeys)
}
