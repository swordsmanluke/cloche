package module_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/module"
)

func TestModulePaths(t *testing.T) {
	proj := "/home/u/proj"
	vendored := &module.Module{Manifest: module.Manifest{Name: "v"}, Dir: proj + "/.cloche/modules/v"}
	inTree := &module.Module{Manifest: module.Manifest{Name: "t"}, Dir: proj + "/tools/t", Local: true}
	external := &module.Module{Manifest: module.Manifest{Name: "e"}, Dir: "/opt/shared/e", Local: true}

	assert.Equal(t, "/workspace/.cloche/modules/v", vendored.ContainerDir(proj))
	assert.Equal(t, "/workspace/tools/t", inTree.ContainerDir(proj))
	assert.Equal(t, "/workspace/.cloche/modules/e", external.ContainerDir(proj))
	assert.False(t, vendored.External(proj))
	assert.False(t, inTree.External(proj))
	assert.True(t, external.External(proj))

	reg := &module.Registry{Modules: map[string]*module.Module{"v": vendored, "t": inTree, "e": external}}
	assert.Equal(t, []*module.Module{external}, reg.ExternalModules(proj))
	assert.Nil(t, (*module.Registry)(nil).ExternalModules(proj))
}

func TestResolveFileRef(t *testing.T) {
	proj := "/p"
	m := &module.Module{Manifest: module.Manifest{Name: "e"}, Dir: "/opt/e", Local: true}
	assert.Equal(t, `file("/opt/e/prompts/x.md")`, m.ResolveFileRef(`file("prompts/x.md")`, proj, false))
	assert.Equal(t, `file(".cloche/modules/e/prompts/x.md")`, m.ResolveFileRef(`file("prompts/x.md")`, proj, true))
	assert.Equal(t, `file("/abs/x.md")`, m.ResolveFileRef(`file("/abs/x.md")`, proj, true))
	assert.Equal(t, "echo hi", m.ResolveFileRef("echo hi", proj, false))
}

func TestSeedWith(t *testing.T) {
	kv, env := module.SeedWith([]domain.WithEntry{
		{Key: "task_id", Source: domain.WithKV, Value: "ticket_id"},
		{Key: "gone", Source: domain.WithKV, Value: "unset"},
		{Key: "MODEL", Env: true, Source: domain.WithLiteral, Value: "opus"},
		{Key: "URL", Env: true, Source: domain.WithKV, Value: "jira_url"},
		{Key: "out_k", Source: domain.WithOut, Value: "o"},
	}, func(k string) (string, bool) {
		v, ok := map[string]string{"ticket_id": "T-1", "jira_url": "http://j"}[k]
		return v, ok
	})
	assert.Equal(t, map[string]string{"task_id": "T-1"}, kv)
	assert.Equal(t, map[string]string{"MODEL": "opus", "URL": "http://j"}, env)
}

func TestCheckContract(t *testing.T) {
	c := &domain.Contract{KV: []string{"a"}, Env: []string{"E"}, Out: []string{"o"}}
	none := func(string) bool { return false }
	all := func(string) bool { return true }

	err := module.CheckInputs("code-review", c, none, all)
	require.Error(t, err)
	assert.Equal(t, "contract: code-review requires kv:a (not set)", err.Error())
	assert.NoError(t, module.CheckInputs("code-review", c, all, all))
	assert.NoError(t, module.CheckInputs("x", nil, none, none))

	err = module.CheckOutputs("code-review", c, none)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out:o")
	assert.NoError(t, module.CheckOutputs("code-review", c, all))
}

func TestEnvContext(t *testing.T) {
	ctx := module.WithEnv(context.Background(), map[string]string{"A": "1"})
	ctx = module.WithEnv(ctx, map[string]string{"B": "2", "A": "3"})
	assert.Equal(t, []string{"A=3", "B=2"}, module.EnvList(module.EnvFromContext(ctx)))
}
