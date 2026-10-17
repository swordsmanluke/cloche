package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRootRepositoryNames(t *testing.T) {
	cfg := &Config{Repositories: []RepositoryConfig{
		{Name: "manager", Path: "."},
		{Name: "game", Path: "./repos/game"},
		{Name: "self", Path: "/proj/"},
	}}
	assert.Equal(t, []string{"manager", "self"}, cfg.RootRepositoryNames("/proj"))

	assert.Nil(t, (&Config{Repositories: []RepositoryConfig{{Name: "game", Path: "./repos/game"}}}).RootRepositoryNames("/proj"))
	assert.Nil(t, (&Config{}).RootRepositoryNames("/proj"), "legacy single-tree project: no named root repo")
	assert.Nil(t, (*Config)(nil).RootRepositoryNames("/proj"))
}
