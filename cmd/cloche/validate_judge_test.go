package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/module/qaconformance"
)

func qaBoundProject(t *testing.T, withJudge bool) string {
	dir := t.TempDir()
	mods := filepath.Join(dir, ".cloche", "modules")
	modDir, err := qaconformance.InstallStubModule(mods)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "project.cloche"), []byte("bind qa = stub\n"), 0o644))
	if !withJudge {
		require.NoError(t, os.Remove(filepath.Join(modDir, "judge.md")))
	}
	return dir
}

func TestValidateQAJudgeWarning(t *testing.T) {
	t.Run("warns without judge.md", func(t *testing.T) {
		w := validateQAJudgeWarnings(qaBoundProject(t, false))
		require.Len(t, w, 1)
		assert.Contains(t, w[0], "no judge.md")
	})
	t.Run("silent with judge.md", func(t *testing.T) {
		assert.Empty(t, validateQAJudgeWarnings(qaBoundProject(t, true)))
	})
	t.Run("silent when qa is unbound", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))
		assert.Empty(t, validateQAJudgeWarnings(dir))
	})
}
