package scan_test

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/intent/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinWorkflow_Validates(t *testing.T) {
	wf := scan.BuiltinWorkflow()

	require.NoError(t, wf.Validate())
	assert.Empty(t, wf.ValidateConfig())
}

func TestBuiltinWorkflow_Shape(t *testing.T) {
	wf := scan.BuiltinWorkflow()

	assert.Equal(t, "intent-scan", wf.Name)
	assert.True(t, wf.Builtin)
	assert.Equal(t, "discover-domains", wf.EntryStep)
	assert.Len(t, wf.Steps, 5)

	wantWires := map[string]string{
		"discover-domains:success": "collect-sources",
		"discover-domains:fail":    "abort",
		"collect-sources:success":  "extract",
		"collect-sources:none":     "done",
		"collect-sources:fail":     "abort",
		"extract:success":          "reconcile",
		"extract:fail":             "abort",
		"reconcile:success":        "apply-reconcile",
		"reconcile:fail":           "abort",
		"apply-reconcile:success":  "done",
		"apply-reconcile:fail":     "abort",
	}
	assert.Len(t, wf.Wiring, len(wantWires))
	for _, wire := range wf.Wiring {
		key := wire.From + ":" + wire.Result
		want, ok := wantWires[key]
		if assert.True(t, ok, "unexpected wire %s", key) {
			assert.Equal(t, want, wire.To, "wire %s", key)
		}
	}
}

// TestBuiltinWorkflow_ScriptsAreDashCompatible guards against a regression
// where the collect-sources / apply-reconcile scripts used the bashism
// `set -o pipefail`, which fails instantly under Ubuntu's default `/bin/sh`
// (dash), which lacks it — the host executor always runs step "run" scripts
// via `sh -c`, not bash. See cloche-la93 x cloche-ulid integration bug.
func TestBuiltinWorkflow_ScriptsAreDashCompatible(t *testing.T) {
	wf := scan.BuiltinWorkflow()

	for _, name := range []string{"collect-sources", "apply-reconcile"} {
		step, ok := wf.Steps[name]
		require.True(t, ok, "step %s should exist", name)
		script := step.Config["run"]
		require.NotEmpty(t, script, "step %s should have a run script", name)

		firstLine, _, _ := strings.Cut(script, "\n")
		cmd := exec.Command("sh", "-c", firstLine)
		out, err := cmd.CombinedOutput()
		assert.NoError(t, err, "step %s: %q failed under sh: %s", name, firstLine, out)
	}
}

func TestBuiltinWorkflow_IndependentInstances(t *testing.T) {
	a := scan.BuiltinWorkflow()
	b := scan.BuiltinWorkflow()

	mapPtr := func(m map[string]string) uintptr { return reflect.ValueOf(m).Pointer() }
	assert.NotEqual(t, mapPtr(a.Config), mapPtr(b.Config))
	for name := range a.Steps {
		assert.NotEqualf(t, mapPtr(a.Steps[name].Config), mapPtr(b.Steps[name].Config), "step %s Config", name)
	}

	a.Steps["extract"].Config["timeout"] = "mutated"
	assert.NotEqual(t, "mutated", b.Steps["extract"].Config["timeout"])
}
