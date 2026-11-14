package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/module/qaconformance"
)

func qaProject(t *testing.T, bind string) string {
	dir := t.TempDir()
	_, err := qaconformance.InstallStubModule(filepath.Join(dir, ".cloche", "modules"))
	require.NoError(t, err)
	if bind != "" {
		writeFile(t, filepath.Join(dir, ".cloche", "project.cloche"), bind)
	}
	return dir
}

func TestRunQuestionPhase_UnboundNamesTheBind(t *testing.T) {
	dir := qaProject(t, "")
	_, err := RunQuestionPhase(context.Background(), &Runner{Store: newSQLiteStore(t)}, dir, SlotAsk, map[string]string{"questions_file": "/x"})
	require.ErrorIs(t, err, ErrNoQABind)
	assert.Contains(t, err.Error(), "bind qa = <module>")
	_, err = QAGuide(dir, false)
	assert.ErrorIs(t, err, ErrNoQABind)
}

func TestQAGuide_ServesBoundModuleDocs(t *testing.T) {
	dir := qaProject(t, "bind qa = stub\n")
	ask, err := QAGuide(dir, false)
	require.NoError(t, err)
	assert.Contains(t, ask, "one concrete question")
	judge, err := QAGuide(dir, true)
	require.NoError(t, err)
	assert.Contains(t, judge, "repository")
}

const qaTrueWF = `workflow ask {
  host {}
  contract {
    kv:  questions_file
    env: QA_PROJECT
    out: question_ids
  }
  step s { run = "true"
 results = [success, fail] }
  s:success -> done
  s:fail -> abort
}
workflow await-answer {
  host {}
  contract {
    kv:  question_ids
    out: questions_open
  }
  step s { run = "true"
 results = [success, fail] }
  s:success -> done
  s:fail -> abort
}
workflow read-answer {
  host {}
  contract {
    kv:  question_id
    out: answer_path
  }
  step s { run = "true"
 results = [success, fail] }
  s:success -> done
  s:fail -> abort
}
`

// Each qa slot resolves through `bind qa = <module>` to the module's export,
// its contract inputs are enforced, and bind env satisfies env requirements.
// The workflows' outputs are faked by outputStore (their steps are `true`).
func TestRunQuestionPhase_RunsBoundModuleSlots(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, ".cloche", "modules", "qa")
	writeFile(t, filepath.Join(mod, "module.toml"), "name = \"qa\"\ndescription = \"t\"\nexports = [\"ask\", \"await-answer\", \"read-answer\"]\n")
	writeFile(t, filepath.Join(mod, "qa.cloche"), qaTrueWF)
	writeFile(t, filepath.Join(dir, ".cloche", "project.cloche"), "bind qa = qa { env.QA_PROJECT = \"p\" }\n")
	os.Unsetenv("QA_PROJECT")

	for slot, tc := range map[string]struct{ in, out map[string]string }{
		SlotAsk:         {map[string]string{"questions_file": "/q.json"}, map[string]string{"question_ids": "Q1\nQ2"}},
		SlotAwaitAnswer: {map[string]string{"question_ids": "Q1"}, map[string]string{"questions_open": ""}},
		SlotReadAnswer:  {map[string]string{"question_id": "Q1"}, map[string]string{"answer_path": "/a"}},
	} {
		store := outputStore{newSQLiteStore(t), tc.out}
		out, err := RunQuestionPhase(context.Background(), &Runner{Store: store}, dir, slot, tc.in)
		require.NoError(t, err, slot)
		assert.Equal(t, tc.out, out, slot)

		_, err = RunQuestionPhase(context.Background(), &Runner{Store: store}, dir, slot, map[string]string{})
		require.Error(t, err, slot)
		assert.Contains(t, err.Error(), "contract", slot)
	}
}

// A read-answer workflow that ends in a failure state (no answer yet) is
// reported as ErrPhaseFailed so the route can answer 404.
func TestRunQuestionPhase_FailedPhaseIsErrPhaseFailed(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, ".cloche", "modules", "qa")
	writeFile(t, filepath.Join(mod, "module.toml"), "name = \"qa\"\ndescription = \"t\"\nexports = [\"read-answer\"]\n")
	writeFile(t, filepath.Join(mod, "qa.cloche"), "workflow read-answer {\n  host {}\n  contract {\n    kv: question_id\n    out: answer_path\n  }\n  step s {\n    run = \"false\"\n    results = [success, fail]\n  }\n  s:success -> done\n  s:fail -> abort\n}\n")
	writeFile(t, filepath.Join(dir, ".cloche", "project.cloche"), "bind qa = qa\n")
	_, err := RunQuestionPhase(context.Background(), &Runner{Store: newSQLiteStore(t)}, dir, SlotReadAnswer, map[string]string{"question_id": "Q1"})
	assert.ErrorIs(t, err, ErrPhaseFailed)
}

// A failed phase carries the failing step and its output, and logs it to the
// calling run's attempt directory only when that directory exists.
func TestRunQuestionPhase_FailureCarriesStepOutputAndLogsToCaller(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".cloche", "qa.cloche"), `workflow ask {
  host {}
  contract {
    kv: questions_file
  }
  step ask {
    run = "echo boom: no such feature; exit 1"
    results = [success, fail]
  }
  ask:success -> done
  ask:fail -> abort
}
`)
	attempt := filepath.Join(dir, ".cloche", "logs", "T1", "A1")
	require.NoError(t, os.MkdirAll(attempt, 0o755))
	ctx := WithCaller(context.Background(), Caller{TaskID: "T1", AttemptID: "A1"})

	_, err := RunQuestionPhase(ctx, &Runner{Store: newSQLiteStore(t)}, dir, SlotAsk, map[string]string{"questions_file": "/x"})
	var pf *PhaseFailure
	require.ErrorAs(t, err, &pf)
	assert.ErrorIs(t, err, ErrPhaseFailed)
	assert.Equal(t, "ask", pf.Step)
	assert.Contains(t, pf.OutputTail, "boom: no such feature")
	assert.Equal(t, filepath.Join(attempt, "ask.log"), pf.LogPath)
	full, err := os.ReadFile(filepath.Join(attempt, "full.log"))
	require.NoError(t, err)
	assert.Contains(t, string(full), "boom: no such feature")

	// A caller whose attempt directory is gone gets no log, and none is created.
	ctx = WithCaller(context.Background(), Caller{TaskID: "T1", AttemptID: "missing"})
	_, err = RunQuestionPhase(ctx, &Runner{Store: newSQLiteStore(t)}, dir, SlotAsk, map[string]string{"questions_file": "/x"})
	require.ErrorAs(t, err, &pf)
	assert.Empty(t, pf.LogPath)
	assert.NoDirExists(t, filepath.Join(dir, ".cloche", "logs", "T1", "missing"))
}
