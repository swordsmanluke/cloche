package grpc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/docker"
	"github.com/swordsmanluke/cloche/internal/domain"
)

// attemptStore is a run store that serves attempts, so tests can give a run a
// repo selection.
type attemptStore struct {
	*fakeRunStore
	attempts map[string]*domain.Attempt
	keys     map[string]string
}

func newAttemptStore(attempts ...*domain.Attempt) *attemptStore {
	s := &attemptStore{fakeRunStore: &fakeRunStore{}, attempts: map[string]*domain.Attempt{}, keys: map[string]string{}}
	for _, a := range attempts {
		s.attempts[a.ID] = a
	}
	return s
}

func (s *attemptStore) GetAttempt(_ context.Context, id string) (*domain.Attempt, error) {
	if a, ok := s.attempts[id]; ok {
		return a, nil
	}
	return s.fakeRunStore.GetAttempt(context.Background(), id)
}

func (s *attemptStore) SetContextKey(_ context.Context, _, _, _, key, value string) error {
	s.keys[key] = value
	return nil
}

func (s *attemptStore) SaveAttempt(_ context.Context, a *domain.Attempt) error {
	s.attempts[a.ID] = a
	return nil
}

func writeRepoConfig(t *testing.T, dir, toml string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "config.toml"), []byte(toml), 0644))
}

const twoRepoConfig = `
[[repositories]]
name = "manager"
path = "."

[[repositories]]
name = "pickup"
path = "./repos/pickup"

[[repositories]]
name = "voxline"
path = "./repos/voxline"
`

func TestAttemptRepoSelection_UsesTheAttemptsRepos(t *testing.T) {
	dir := t.TempDir()
	writeRepoConfig(t, dir, twoRepoConfig)
	d := &DaemonExecutor{
		projectDir: dir,
		attemptID:  "at01",
		store:      newAttemptStore(&domain.Attempt{ID: "at01", Repos: []string{"pickup"}}),
	}

	sel, err := d.attemptRepoSelection(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"pickup"}, sel.CopyNames())
	assert.Equal(t, []string{"repos/voxline"}, sel.ExcludePaths())
	assert.Contains(t, sel.CopyOnly(), ".cloche")
}

func TestAttemptRepoSelection_NoListMeansEveryRepo(t *testing.T) {
	dir := t.TempDir()
	writeRepoConfig(t, dir, twoRepoConfig)
	d := &DaemonExecutor{
		projectDir: dir,
		attemptID:  "at01",
		store:      newAttemptStore(&domain.Attempt{ID: "at01"}),
	}

	sel, err := d.attemptRepoSelection(context.Background())
	require.NoError(t, err)

	assert.Nil(t, sel.CopyNames())
	assert.Empty(t, sel.ExcludePaths())
	assert.Nil(t, sel.CopyOnly())
}

func TestAttemptRepoSelection_NoConfigIsASingleTreeProject(t *testing.T) {
	d := &DaemonExecutor{projectDir: t.TempDir()}

	sel, err := d.attemptRepoSelection(context.Background())
	require.NoError(t, err)

	assert.Nil(t, sel.CopyOnly())
	require.Len(t, sel.Extract, 1)
}

func TestExecuteWorkflowStep_UnknownRepoFailsTheStepBeforeAnyContainerWork(t *testing.T) {
	dir := t.TempDir()
	writeRepoConfig(t, dir, twoRepoConfig)
	wf := &domain.Workflow{Name: "develop", Location: domain.LocationContainer}
	store := newAttemptStore(&domain.Attempt{ID: "at01", Repos: []string{"pikcup"}})
	pool := docker.NewContainerPool(&errContainerRuntime{err: fmt.Errorf("no container should start")})
	d := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir: dir,
		Store:      store,
		AttemptID:  "at01",
		TaskID:     "t1",
		Pool:       pool,
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})
	defer d.Close(true)

	result, err := d.executeWorkflowStep(context.Background(), &domain.Step{
		Name:   "develop",
		Config: map[string]string{"workflow_name": "develop"},
	})

	require.NoError(t, err, "a bad list fails through the workflow's fail wiring")
	assert.Equal(t, "fail", result.Result)
	assert.Empty(t, d.worktrees, "no extract worktrees for a run that can't start")
	assert.Contains(t, store.keys[domain.RepoErrorKey], `unknown repo "pikcup"`)
}
