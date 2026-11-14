package host

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgeLauncher_ClosedGateStartsNothing(t *testing.T) {
	dir := qaProject(t, "bind qa = stub\n")
	built := false
	l := &JudgeLauncher{
		Blocked:   func() bool { return true },
		NewRunner: func(string) *Runner { built = true; return &Runner{} },
	}
	require.NoError(t, l.LaunchJudge(context.Background(), dir, []string{"Q1"}))
	l.Wait()
	assert.False(t, built, "no runner (so no run) when the gate is closed")
}

func TestJudgeLauncher_NoJudgeMDIsAnErrorNotARun(t *testing.T) {
	dir := t.TempDir() // no qa bind, hence no judge.md
	l := &JudgeLauncher{NewRunner: func(string) *Runner { t.Fatal("must not build a runner"); return nil }}
	assert.Error(t, l.LaunchJudge(context.Background(), dir, []string{"Q1"}))
}
