package module_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sagaEnv builds a sandbox: a stub saga (prints $SAGA_STUB_OUT, logs args) and a
// stub kv tool backed by files.
func sagaEnv(t *testing.T, sagaOut string) (env []string, kvDir, log string) {
	t.Helper()
	dir := t.TempDir()
	kvDir = filepath.Join(dir, "kvstore")
	require.NoError(t, os.MkdirAll(kvDir, 0o755))
	log = filepath.Join(dir, "saga.log")
	// Like the real CLI: ask/poll need --json (otherwise they print markdown, which
	// the scripts cannot parse); poll exits with $SAGA_STUB_RC (2 = still open).
	write(t, filepath.Join(dir, "saga"), `#!/usr/bin/env bash
echo "$@" >> "`+log+`"
case "$1" in
  ask|poll)
    case " $* " in *" --json "*) ;; *) echo "# markdown output"; exit 0 ;; esac ;;
esac
if [ "$1" = ask ]; then
  case " $* " in *" -p "*" -f "*" --file "*) ;; *) echo "missing -p/-f/--file" >&2; exit 1 ;; esac
fi
cat "$SAGA_STUB_OUT"
exit "${SAGA_STUB_RC:-0}"
`)
	write(t, filepath.Join(dir, "kv"), `#!/usr/bin/env bash
case "$1" in
  get) cat "$KV_DIR/$2" 2>/dev/null ;;
  set) printf '%s' "$3" > "$KV_DIR/$2" ;;
esac
`)
	require.NoError(t, os.Chmod(filepath.Join(dir, "saga"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(dir, "kv"), 0o755))
	out := filepath.Join(dir, "out")
	write(t, out, sagaOut)
	env = append(os.Environ(), "SAGA_BIN="+filepath.Join(dir, "saga"), "SAGA_JUDGE_KV_BIN="+filepath.Join(dir, "kv"),
		"KV_DIR="+kvDir, "SAGA_STUB_OUT="+out, "SAGA_BATCH_ID=", "SAGA_BATCH_FILE=")
	return env, kvDir, log
}

func runSagaScript(t *testing.T, name string, env []string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", ".cloche", "modules", "saga-judge", "scripts", name))
	require.NoError(t, err)
	cmd := exec.Command("bash", script)
	cmd.Env = env
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func kvRead(t *testing.T, dir, key string) string {
	b, err := os.ReadFile(filepath.Join(dir, key))
	require.NoError(t, err)
	return string(b)
}

func TestSagaAsk_StoresBatchID(t *testing.T) {
	env, kv, log := sagaEnv(t, `{"batch":"CLOCHE-B42","questions":["CLOCHE-Q1"]}`)
	env = append(env, "SAGA_PROJECT=cloche", "SAGA_FEATURE=loop")
	batch := filepath.Join(t.TempDir(), "batch.json")
	write(t, batch, `{"title":"t","questions":[]}`)
	write(t, filepath.Join(kv, "saga_batch_file"), batch)

	out, err := runSagaScript(t, "ask.sh", env)
	require.NoError(t, err, out)
	assert.Equal(t, "CLOCHE-B42", kvRead(t, kv, "saga_batch_id"))
	logged, _ := os.ReadFile(log)
	assert.Equal(t, "ask -p cloche -f loop --file "+batch+" --json\n", string(logged))
}

func TestSagaAsk_Failures(t *testing.T) {
	env, _, _ := sagaEnv(t, `{"batch":"CLOCHE-B1"}`)
	env = append(env, "SAGA_PROJECT=p", "SAGA_FEATURE=f")
	_, err := runSagaScript(t, "ask.sh", env) // no batch file in kv
	assert.Error(t, err)

	env, kv, _ := sagaEnv(t, `{"batch":"CLOCHE-B1"}`)
	batch := filepath.Join(t.TempDir(), "batch.json")
	write(t, batch, "{}")
	write(t, filepath.Join(kv, "saga_batch_file"), batch)
	out, err := runSagaScript(t, "ask.sh", env) // no project/feature
	assert.Error(t, err)
	assert.Contains(t, out, "project and feature")

	env, kv, _ = sagaEnv(t, `{"nothing":true}`)
	env = append(env, "SAGA_PROJECT=p", "SAGA_FEATURE=f")
	write(t, filepath.Join(kv, "saga_batch_file"), batch)
	_, err = runSagaScript(t, "ask.sh", env) // no id in output
	assert.Error(t, err)
}

func sagaPollJSON(done bool, states ...string) string {
	qs := make([]string, len(states))
	for i, s := range states {
		qs[i] = `{"id":"CLOCHE-Q` + string(rune('1'+i)) + `","batch":"CLOCHE-B1","status":{"state":"` + s + `"}}`
	}
	d := "false"
	if done {
		d = "true"
	}
	return `{"done":` + d + `,"questions":[` + strings.Join(qs, ",") + `]}`
}

func TestSagaPoll_Open(t *testing.T) {
	env, kv, log := sagaEnv(t, sagaPollJSON(false, "in-force", "open"))
	env = append(env, "SAGA_STUB_RC=2")
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	out, err := runSagaScript(t, "poll.sh", env)
	require.NoError(t, err, out) // exit 2 is "still open", not an error
	assert.NotContains(t, out, "CLOCHE_RESULT")
	logged, _ := os.ReadFile(log)
	assert.Equal(t, "poll CLOCHE-B1 --no-wait --json\n", string(logged))
}

func TestSagaPoll_Done(t *testing.T) {
	env, kv, _ := sagaEnv(t, sagaPollJSON(true, "in-force", "in-force"))
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	out, err := runSagaScript(t, "poll.sh", env)
	require.NoError(t, err, out)
	assert.Contains(t, out, "CLOCHE_RESULT:answered")
	assert.Equal(t, "answered", kvRead(t, kv, "saga_outcome"))
}

func TestSagaPoll_Reframe(t *testing.T) {
	env, kv, _ := sagaEnv(t, sagaPollJSON(false, "in-force", "deprecated", "open"))
	env = append(env, "SAGA_STUB_RC=2")
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	out, err := runSagaScript(t, "poll.sh", env)
	require.NoError(t, err, out)
	assert.Contains(t, out, "CLOCHE_RESULT:reframe")
	assert.Equal(t, "reframe", kvRead(t, kv, "saga_outcome"))
}

func TestSagaPoll_Failure(t *testing.T) {
	env, kv, _ := sagaEnv(t, "boom")
	env = append(env, "SAGA_STUB_RC=1")
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	out, err := runSagaScript(t, "poll.sh", env)
	assert.Error(t, err, "saga exit 1 must fail the step")
	assert.NotContains(t, out, "CLOCHE_RESULT")

	env, kv, _ = sagaEnv(t, `{"done":true,"questions":[]}`)
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	_, err = runSagaScript(t, "poll.sh", env)
	assert.Error(t, err, "empty batch must fail, not report answered")

	env, _, _ = sagaEnv(t, `{}`)
	_, err = runSagaScript(t, "poll.sh", env) // no batch id
	assert.Error(t, err)
}

func TestSagaJudgePrepare_OnlyOpenQuestions(t *testing.T) {
	env, kv, _ := sagaEnv(t, `{"done":false,"questions":[{"id":"a","status":{"state":"open"}},{"id":"b","status":{"state":"held"}},{"id":"c","status":{"state":"in-force"}}]}`)
	env = append(env, "SAGA_STUB_RC=2")
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	tmp := t.TempDir()
	env = append(env, "SAGA_JUDGE_TMP="+tmp)
	_, err := runSagaScript(t, "judge-prepare.sh", env)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmp, "judge-questions.json"), kvRead(t, kv, "judge_questions_path"))
	data, _ := os.ReadFile(filepath.Join(tmp, "judge-questions.json"))
	assert.Contains(t, string(data), `"a"`)
	assert.NotContains(t, string(data), `"b"`)
	assert.NotContains(t, string(data), `"c"`)
}
