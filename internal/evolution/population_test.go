package evolution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPopulation(t *testing.T) *Population {
	t.Helper()
	return &Population{
		ProjectDir: t.TempDir(),
		StepName:   "code",
	}
}

func TestAddCandidate(t *testing.T) {
	pop := newTestPopulation(t)

	id1, err := pop.AddCandidate("prompt version 1", "")
	require.NoError(t, err)
	assert.Equal(t, "candidate-001", id1)

	id2, err := pop.AddCandidate("prompt version 2", id1)
	require.NoError(t, err)
	assert.Equal(t, "candidate-002", id2)

	// Verify files exist on disk.
	_, err = os.Stat(pop.candidatePath(id1))
	assert.NoError(t, err)
	_, err = os.Stat(pop.candidatePath(id2))
	assert.NoError(t, err)
	_, err = os.Stat(pop.metaPath())
	assert.NoError(t, err)
}

func TestAddCandidateParentID(t *testing.T) {
	pop := newTestPopulation(t)

	id1, err := pop.AddCandidate("base prompt", "")
	require.NoError(t, err)

	id2, err := pop.AddCandidate("derived prompt", id1)
	require.NoError(t, err)

	all, err := pop.readMeta()
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "", all[0].ParentID)
	assert.Equal(t, id1, all[1].ParentID)
	_ = id2
}

func TestListCandidatesFiltersActive(t *testing.T) {
	pop := newTestPopulation(t)

	_, err := pop.AddCandidate("v1", "")
	require.NoError(t, err)
	_, err = pop.AddCandidate("v2", "")
	require.NoError(t, err)

	// Manually archive one.
	all, err := pop.readMeta()
	require.NoError(t, err)
	all[0].Status = candidateStatusArchived
	require.NoError(t, pop.writeMeta(all))

	active, err := pop.ListCandidates()
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "candidate-002", active[0].ID)
}

func TestListCandidatesEmpty(t *testing.T) {
	pop := newTestPopulation(t)

	active, err := pop.ListCandidates()
	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestGetContent(t *testing.T) {
	pop := newTestPopulation(t)

	id, err := pop.AddCandidate("hello world prompt", "")
	require.NoError(t, err)

	content, err := pop.GetContent(id)
	require.NoError(t, err)
	assert.Equal(t, "hello world prompt", content)
}

func TestGetContentNotFound(t *testing.T) {
	pop := newTestPopulation(t)

	_, err := pop.GetContent("candidate-999")
	assert.Error(t, err)
}

func TestPromote(t *testing.T) {
	pop := newTestPopulation(t)

	basePath := filepath.Join(pop.ProjectDir, ".cloche", "prompts", "code.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0755))
	require.NoError(t, os.WriteFile(basePath, []byte("original base"), 0644))

	id1, err := pop.AddCandidate("variant A", "")
	require.NoError(t, err)
	id2, err := pop.AddCandidate("variant B", "")
	require.NoError(t, err)

	err = pop.Promote(id2, basePath)
	require.NoError(t, err)

	// Base file should now contain winner content.
	data, err := os.ReadFile(basePath)
	require.NoError(t, err)
	assert.Equal(t, "variant B", string(data))

	// Snapshot directory should have the old base.
	snapDir := filepath.Join(pop.ProjectDir, ".cloche", "evolution", "snapshots")
	entries, err := os.ReadDir(snapDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	snapData, err := os.ReadFile(filepath.Join(snapDir, entries[0].Name()))
	require.NoError(t, err)
	assert.Equal(t, "original base", string(snapData))

	// Check statuses.
	all, err := pop.readMeta()
	require.NoError(t, err)
	for _, m := range all {
		if m.ID == id2 {
			assert.Equal(t, candidateStatusPromoted, m.Status)
		} else if m.ID == id1 {
			assert.Equal(t, candidateStatusArchived, m.Status)
		}
	}
}

func TestPromoteInvalidCandidate(t *testing.T) {
	pop := newTestPopulation(t)

	err := pop.Promote("candidate-999", "/tmp/base.md")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not active")
}

func TestPromoteNoExistingBase(t *testing.T) {
	pop := newTestPopulation(t)

	basePath := filepath.Join(pop.ProjectDir, ".cloche", "prompts", "code.md")

	id, err := pop.AddCandidate("first ever prompt", "")
	require.NoError(t, err)

	err = pop.Promote(id, basePath)
	require.NoError(t, err)

	// Base file should be created with winner content.
	data, err := os.ReadFile(basePath)
	require.NoError(t, err)
	assert.Equal(t, "first ever prompt", string(data))
}

func TestRecordAndGetAssignment(t *testing.T) {
	pop := newTestPopulation(t)

	id, err := pop.AddCandidate("prompt v1", "")
	require.NoError(t, err)

	err = pop.RecordAssignment("run-abc", id)
	require.NoError(t, err)

	got, err := pop.GetAssignment("run-abc")
	require.NoError(t, err)
	assert.Equal(t, id, got)
}

func TestGetAssignmentNotFound(t *testing.T) {
	pop := newTestPopulation(t)

	got, err := pop.GetAssignment("run-xyz")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestGetAssignmentNoFile(t *testing.T) {
	pop := newTestPopulation(t)

	got, err := pop.GetAssignment("run-nope")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestMultipleAssignments(t *testing.T) {
	pop := newTestPopulation(t)

	id1, err := pop.AddCandidate("p1", "")
	require.NoError(t, err)
	id2, err := pop.AddCandidate("p2", "")
	require.NoError(t, err)

	require.NoError(t, pop.RecordAssignment("run-1", id1))
	require.NoError(t, pop.RecordAssignment("run-2", id2))
	require.NoError(t, pop.RecordAssignment("run-3", id1))

	got1, err := pop.GetAssignment("run-1")
	require.NoError(t, err)
	assert.Equal(t, id1, got1)

	got2, err := pop.GetAssignment("run-2")
	require.NoError(t, err)
	assert.Equal(t, id2, got2)

	got3, err := pop.GetAssignment("run-3")
	require.NoError(t, err)
	assert.Equal(t, id1, got3)
}

func TestDiskLayout(t *testing.T) {
	pop := newTestPopulation(t)

	_, err := pop.AddCandidate("content", "")
	require.NoError(t, err)

	// Verify expected directory structure.
	popDir := filepath.Join(pop.ProjectDir, ".cloche", "evolution", "population", "code")
	_, err = os.Stat(popDir)
	assert.NoError(t, err)

	_, err = os.Stat(filepath.Join(popDir, "candidate-001.md"))
	assert.NoError(t, err)

	_, err = os.Stat(filepath.Join(popDir, "meta.jsonl"))
	assert.NoError(t, err)
}
