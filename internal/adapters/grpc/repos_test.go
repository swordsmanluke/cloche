package grpc

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
)

func TestResolveReposLegacyNoConfig(t *testing.T) {
	wf := &domain.Workflow{Name: "develop"}
	got, err := resolveRepos(wf, nil, "/proj")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "", got[0].Name)
	assert.Equal(t, "/proj", got[0].Path)
	assert.Equal(t, "", got[0].SubPath)
}

func TestResolveReposLegacyEmptyConfigRepositories(t *testing.T) {
	wf := &domain.Workflow{Name: "develop"}
	cfg := &config.Config{}
	got, err := resolveRepos(wf, cfg, "/proj")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "/proj", got[0].Path)
}

func TestResolveReposDefaultsToAllConfigured(t *testing.T) {
	wf := &domain.Workflow{Name: "develop"} // no Repos = default to all
	cfg := &config.Config{Repositories: []config.RepositoryConfig{
		{Name: "a", Path: "./repos/a"},
		{Name: "b", Path: "./repos/b"},
	}}
	got, err := resolveRepos(wf, cfg, "/proj")
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "a", got[0].Name)
	assert.Equal(t, filepath.Join("/proj", "repos/a"), got[0].Path)
	assert.Equal(t, "repos/a", got[0].SubPath)

	assert.Equal(t, "b", got[1].Name)
	assert.Equal(t, filepath.Join("/proj", "repos/b"), got[1].Path)
	assert.Equal(t, "repos/b", got[1].SubPath)
}

func TestResolveReposDeclaredSubset(t *testing.T) {
	wf := &domain.Workflow{Name: "develop", Repos: []string{"b"}}
	cfg := &config.Config{Repositories: []config.RepositoryConfig{
		{Name: "a", Path: "./repos/a"},
		{Name: "b", Path: "./repos/b"},
	}}
	got, err := resolveRepos(wf, cfg, "/proj")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].Name)
}

func TestResolveReposUnknownRepoNameErrors(t *testing.T) {
	wf := &domain.Workflow{Name: "develop", Repos: []string{"ghost"}}
	cfg := &config.Config{Repositories: []config.RepositoryConfig{
		{Name: "a", Path: "./repos/a"},
	}}
	_, err := resolveRepos(wf, cfg, "/proj")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghost")
}

func TestResolveReposAbsolutePathRespected(t *testing.T) {
	wf := &domain.Workflow{Name: "develop"}
	cfg := &config.Config{Repositories: []config.RepositoryConfig{
		{Name: "abs", Path: "/somewhere/else"},
	}}
	got, err := resolveRepos(wf, cfg, "/proj")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "/somewhere/else", got[0].Path)
	assert.Equal(t, "/somewhere/else", got[0].SubPath)
}
