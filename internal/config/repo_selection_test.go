package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func managerConfig() *Config {
	return &Config{Repositories: []RepositoryConfig{
		{Name: "manager", Path: "."},
		{Name: "anarkana", Path: "./repos/anarkana"},
		{Name: "mocap", Path: "./repos/mocap", DependsOn: []string{"anarkana"}},
		{Name: "pickup", Path: "./repos/pickup"},
		{Name: "tool", Path: "./repos/tool", DependsOn: []string{"mocap"}},
	}}
}

func repoNames(repos []ResolvedRepo) []string {
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names
}

func TestSelectRepositories_NoNamesSelectsEverythingIncludingListedRoot(t *testing.T) {
	sel, err := managerConfig().SelectRepositories("/p", nil)
	require.NoError(t, err)

	assert.Len(t, sel.Extract, 5)
	assert.Len(t, sel.Copy, 5)
	assert.Nil(t, sel.CopyNames())
	assert.Empty(t, sel.ExcludePaths())
	assert.Nil(t, sel.CopyOnly())
}

func TestSelectRepositories_NarrowsCopyAndExtractAndLimitsRoot(t *testing.T) {
	sel, err := managerConfig().SelectRepositories("/p", []string{"pickup"})
	require.NoError(t, err)

	assert.Equal(t, []string{"pickup"}, repoNames(sel.Extract))
	assert.Equal(t, []string{"pickup"}, sel.CopyNames())
	assert.Equal(t, []string{"repos/anarkana", "repos/mocap", "repos/tool"}, sel.ExcludePaths())
	assert.Equal(t, []string{".claude", ".cloche", ".clocheignore", "AGENTS.md", "CLAUDE.md", "repos/pickup"}, sel.CopyOnly())
}

func TestSelectRepositories_SelectedRootIsCopiedWholeAndExtracted(t *testing.T) {
	sel, err := managerConfig().SelectRepositories("/p", []string{"manager", "pickup"})
	require.NoError(t, err)

	assert.Equal(t, []string{"manager", "pickup"}, repoNames(sel.Extract))
	assert.Nil(t, sel.CopyOnly())
	assert.Equal(t, []string{"repos/anarkana", "repos/mocap", "repos/tool"}, sel.ExcludePaths())
}

func TestSelectRepositories_UnlistedRootIsNeverCopiedWhole(t *testing.T) {
	cfg := &Config{Repositories: []RepositoryConfig{
		{Name: "a", Path: "./repos/a"},
		{Name: "b", Path: "./repos/b"},
	}}

	sel, err := cfg.SelectRepositories("/p", nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, repoNames(sel.Extract))
	assert.Equal(t, []string{".claude", ".cloche", ".clocheignore", "AGENTS.md", "CLAUDE.md", "repos/a", "repos/b"}, sel.CopyOnly())
}

func TestSelectRepositories_DependenciesAreCopiedTransitivelyButNotExtracted(t *testing.T) {
	sel, err := managerConfig().SelectRepositories("/p", []string{"tool"})
	require.NoError(t, err)

	assert.Equal(t, []string{"tool"}, repoNames(sel.Extract))
	assert.Equal(t, []string{"anarkana", "mocap", "tool"}, sel.CopyNames())
	assert.Equal(t, []string{"repos/pickup"}, sel.ExcludePaths())
}

func TestSelectRepositories_NamedDependencyIsExtracted(t *testing.T) {
	sel, err := managerConfig().SelectRepositories("/p", []string{"mocap", "anarkana"})
	require.NoError(t, err)

	assert.Equal(t, []string{"mocap", "anarkana"}, repoNames(sel.Extract))
	assert.Equal(t, []string{"anarkana", "mocap"}, sel.CopyNames())
}

func TestSelectRepositories_DependencyOnRootCopiesItWholeWithoutExtracting(t *testing.T) {
	cfg := &Config{Repositories: []RepositoryConfig{
		{Name: "root", Path: "."},
		{Name: "a", Path: "./repos/a", DependsOn: []string{"root"}},
	}}

	sel, err := cfg.SelectRepositories("/p", []string{"a"})
	require.NoError(t, err)

	assert.Equal(t, []string{"a"}, repoNames(sel.Extract))
	assert.Nil(t, sel.CopyOnly())
}

func TestSelectRepositories_UnknownNameFailsNamingTheValidRepos(t *testing.T) {
	_, err := managerConfig().SelectRepositories("/p", []string{"pikcup"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `"pikcup"`)
	assert.Contains(t, err.Error(), "anarkana, manager, mocap, pickup, tool")
}

func TestSelectRepositories_DuplicateNamesAreCollapsed(t *testing.T) {
	sel, err := managerConfig().SelectRepositories("/p", []string{"pickup", "pickup"})
	require.NoError(t, err)

	assert.Equal(t, []string{"pickup"}, repoNames(sel.Extract))
	assert.Equal(t, []string{"pickup"}, sel.CopyNames())
}

func TestSelectRepositories_SingleTreeProjectIsUnaffected(t *testing.T) {
	for name, cfg := range map[string]*Config{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			sel, err := cfg.SelectRepositories("/p", nil)
			require.NoError(t, err)

			require.Len(t, sel.Extract, 1)
			assert.Equal(t, "/p", sel.Extract[0].Path)
			assert.Nil(t, sel.CopyOnly())
			assert.Nil(t, sel.CopyNames())
			assert.Empty(t, sel.ExcludePaths())
		})
	}
}

func TestSelectRepositories_NamesWithoutRepositoriesConfiguredFail(t *testing.T) {
	_, err := (&Config{}).SelectRepositories("/p", []string{"a"})

	assert.ErrorContains(t, err, "no [[repositories]]")
}

func TestSelection_ReposOutsideTheProjectAreNotCopyPaths(t *testing.T) {
	cfg := &Config{Repositories: []RepositoryConfig{
		{Name: "a", Path: "./repos/a"},
		{Name: "sibling", Path: "../sibling"},
		{Name: "abs", Path: "/elsewhere/abs"},
	}}

	sel, err := cfg.SelectRepositories("/p", []string{"sibling"})
	require.NoError(t, err)

	assert.Equal(t, []string{"repos/a"}, sel.ExcludePaths())
	assert.NotContains(t, sel.CopyOnly(), "../sibling")
}

func TestValidateRepositoryDeps(t *testing.T) {
	assert.NoError(t, managerConfig().ValidateRepositoryDeps())

	unknown := &Config{Repositories: []RepositoryConfig{{Name: "a", Path: "./a", DependsOn: []string{"ghost"}}}}
	assert.ErrorContains(t, unknown.ValidateRepositoryDeps(), `unknown repo "ghost"`)

	cycle := &Config{Repositories: []RepositoryConfig{
		{Name: "a", Path: "./a", DependsOn: []string{"b"}},
		{Name: "b", Path: "./b", DependsOn: []string{"a"}},
	}}
	assert.ErrorContains(t, cycle.ValidateRepositoryDeps(), "a -> b -> a")

	_, err := cycle.SelectRepositories("/p", []string{"a"})
	assert.ErrorContains(t, err, "cycle")
}
