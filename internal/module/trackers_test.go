package module_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/builtin"
	"github.com/swordsmanluke/cloche/internal/module"
)

var trackerSlots = []string{"list-tasks", "claim-task", "close-task", "release-task"}

// copyTrackers copies this repo's tracker modules into a temp project.
func copyTrackers(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"beads-tasks", "github-tasks"} {
		src := filepath.Join("..", "..", ".cloche", "modules", name)
		dst := filepath.Join(dir, ".cloche", "modules", name)
		require.NoError(t, filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			if info.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), data, info.Mode())
		}))
	}
	return dir
}

func bindAll(target string) string {
	s := "bind {\n"
	for _, slot := range trackerSlots {
		s += "  " + slot + " = " + target + "\n"
	}
	return s + "}\n"
}

// TestTrackerModules_SameContract checks both trackers export the standard
// task-management contract and that project.cloche alone swaps them.
func TestTrackerModules_SameContract(t *testing.T) {
	dir := copyTrackers(t)
	for _, tracker := range []string{"beads-tasks", "github-tasks"} {
		write(t, filepath.Join(dir, ".cloche", module.ProjectFile), bindAll(tracker))
		reg, err := module.Load(dir)
		require.NoError(t, err)
		rs := &module.Resolver{Registry: reg, Project: nil, Builtin: builtin.Lookup}
		for _, slot := range trackerSlots {
			res, err := rs.ResolveSlot(nil, slot)
			require.NoError(t, err, "%s/%s", tracker, slot)
			assert.Equal(t, tracker, res.Workflow.Module)
			require.NotNil(t, res.Workflow.Contract, "%s/%s has no contract", tracker, slot)
		}
	}

	beads, github := loadExport(t, dir, "beads-tasks"), loadExport(t, dir, "github-tasks")
	for _, slot := range trackerSlots {
		assert.Equal(t, beads[slot], github[slot], "contract of %s differs between trackers", slot)
	}
	assert.Equal(t, []string{"tasks_path"}, beads["list-tasks"].Out)
	assert.Equal(t, []string{"task_id"}, beads["claim-task"].KV)
	assert.Equal(t, []string{"task_title", "task_body_path"}, beads["claim-task"].Out)
}

func loadExport(t *testing.T, dir, mod string) map[string]*contractShape {
	t.Helper()
	reg, err := module.Load(dir)
	require.NoError(t, err)
	out := map[string]*contractShape{}
	for _, slot := range trackerSlots {
		wf, err := reg.Export(mod, slot)
		require.NoError(t, err)
		require.NotNil(t, wf.Contract)
		out[slot] = &contractShape{KV: wf.Contract.KV, Env: wf.Contract.Env, Out: wf.Contract.Out}
	}
	return out
}

type contractShape struct{ KV, Env, Out []string }

func TestTrackerModules_Check(t *testing.T) {
	dir := copyTrackers(t)
	write(t, filepath.Join(dir, ".cloche", module.ProjectFile), bindAll("github-tasks"))
	rep := module.Check(dir, nil, builtin.Lookup)
	for _, e := range rep.Errors {
		// Host binaries (bd, gh, jq) are environment-dependent.
		assert.Contains(t, e, "required binary", e)
	}
}
