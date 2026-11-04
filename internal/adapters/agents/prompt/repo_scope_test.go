package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func assembleForRepoScope(t *testing.T) string {
	t.Helper()
	a := &Adapter{}
	step := &domain.Step{Name: "implement", Config: map[string]string{"prompt": "Do the work. repos={{ $repos }}"}}
	got, err := a.assemblePrompt(context.Background(), step, t.TempDir(), "nonce")
	require.NoError(t, err)
	return got
}

func TestAssemblePrompt_NamesTheContainersReposWhenNarrowed(t *testing.T) {
	t.Setenv(domain.ReposEnvVar, "pickup,golem-forge")

	got := assembleForRepoScope(t)

	assert.Contains(t, got, "## Repositories in this container")
	assert.Contains(t, got, "only these repositories: pickup, golem-forge")
	assert.Contains(t, got, "report failure and name the repository you need")
	assert.Contains(t, got, "repos=pickup,golem-forge")
}

func TestAssemblePrompt_NoNoticeWithoutASelection(t *testing.T) {
	t.Setenv(domain.ReposEnvVar, "")

	assert.NotContains(t, assembleForRepoScope(t), "## Repositories in this container")
}
