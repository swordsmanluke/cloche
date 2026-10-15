package module_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/module"
)

const wfSrc = `workflow list-tasks {
  step a {
    run = "true"
    results = [success, fail]
  }
  a:success -> done
  a:fail -> abort
}

workflow private {
  step a {
    run = "true"
    results = [success, fail]
  }
  a:success -> done
  a:fail -> abort
}
`

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestLoad_Empty(t *testing.T) {
	reg, err := module.Load(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, reg.Modules)
	assert.Empty(t, reg.Root)
}

func TestLoad_VendoredModule(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, ".cloche", "modules", "jira-tasks")
	write(t, filepath.Join(mod, "module.toml"), `name = "jira-tasks"
description = "Jira"
exports = ["list-tasks"]
requires = ["curl"]
credentials = ["JIRA_TOKEN"]
uses = ["claim-task"]
`)
	write(t, filepath.Join(mod, "tasks.cloche"), "bind lint = rubocop\n"+wfSrc)

	reg, err := module.Load(dir)
	require.NoError(t, err)
	m := reg.Modules["jira-tasks"]
	require.NotNil(t, m)
	assert.Equal(t, "Jira", m.Description)
	assert.Equal(t, []string{"curl"}, m.Requires)
	assert.Equal(t, []string{"JIRA_TOKEN"}, m.Credentials)
	assert.Equal(t, []string{"claim-task"}, m.Uses)
	assert.False(t, m.Local)
	assert.Len(t, m.FileBinds[filepath.Join(mod, "tasks.cloche")], 1)

	wf, err := reg.Export("jira-tasks", "list-tasks")
	require.NoError(t, err)
	assert.Equal(t, "jira-tasks", wf.Module)

	_, err = reg.Export("jira-tasks", "private")
	assert.ErrorContains(t, err, "does not export")
	_, err = reg.Export("nope", "x")
	assert.ErrorContains(t, err, "not installed")
}

func TestLoad_LocalPathModule(t *testing.T) {
	dir := t.TempDir()
	ext := filepath.Join(dir, "shared", "my-review")
	write(t, filepath.Join(ext, "module.toml"), `name = "my-review"`+"\n"+`exports = ["list-tasks"]`)
	write(t, filepath.Join(ext, "r.cloche"), wfSrc)
	write(t, filepath.Join(dir, ".cloche", "modules.lock"), `[[module]]
name = "my-review"
source = "path:shared/my-review"
`)

	reg, err := module.Load(dir)
	require.NoError(t, err)
	m := reg.Modules["my-review"]
	require.NotNil(t, m)
	assert.True(t, m.Local)
	_, err = reg.Export("my-review", "list-tasks")
	assert.NoError(t, err)
}

func TestLoad_LockNameMismatch(t *testing.T) {
	dir := t.TempDir()
	ext := filepath.Join(dir, "ext")
	write(t, filepath.Join(ext, "module.toml"), `name = "other"`)
	write(t, filepath.Join(dir, ".cloche", "modules.lock"), "[[module]]\nname = \"mine\"\nsource = \"path:ext\"\n")
	_, err := module.Load(dir)
	assert.ErrorContains(t, err, "declares name")
}

func TestLoad_Errors(t *testing.T) {
	t.Run("dir name mismatch", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, ".cloche", "modules", "a", "module.toml"), `name = "b"`)
		_, err := module.Load(dir)
		assert.ErrorContains(t, err, "does not match directory")
	})
	t.Run("missing name", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, ".cloche", "modules", "a", "module.toml"), `description = "x"`)
		_, err := module.Load(dir)
		assert.ErrorContains(t, err, "name is required")
	})
	t.Run("duplicate", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, ".cloche", "modules", "a", "module.toml"), `name = "a"`)
		write(t, filepath.Join(dir, "ext", "module.toml"), `name = "a"`)
		write(t, filepath.Join(dir, ".cloche", "modules.lock"), "[[module]]\nname = \"a\"\nsource = \"path:ext\"\n")
		_, err := module.Load(dir)
		assert.ErrorContains(t, err, "duplicate module")
	})
	t.Run("bad toml", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, ".cloche", "modules", "a", "module.toml"), `name = `)
		_, err := module.Load(dir)
		assert.Error(t, err)
	})
}

func TestLoad_ProjectRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".cloche", "project.cloche"), "bind {\n  code-review = ruby-review\n}\nbind! lint = std\n")
	reg, err := module.Load(dir)
	require.NoError(t, err)
	require.Len(t, reg.Root, 2)
	assert.Equal(t, "code-review", reg.Root[0].Slot)
	assert.True(t, reg.Root[1].Force)
}
