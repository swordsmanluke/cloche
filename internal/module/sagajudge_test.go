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
	write(t, filepath.Join(dir, "saga"), "#!/usr/bin/env bash\necho \"$@\" >> \""+log+"\"\ncat \"$SAGA_STUB_OUT\"\n")
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
	env, kv, log := sagaEnv(t, "Asked 2 questions\nBatch CLOCHE-B42\n")
	batch := filepath.Join(t.TempDir(), "batch.md")
	write(t, batch, "q1")
	write(t, filepath.Join(kv, "saga_batch_file"), batch)

	_, err := runSagaScript(t, "ask.sh", env)
	require.NoError(t, err)
	assert.Equal(t, "CLOCHE-B42", kvRead(t, kv, "saga_batch_id"))
	logged, _ := os.ReadFile(log)
	assert.Equal(t, "ask "+batch+"\n", string(logged))
}

func TestSagaAsk_Failures(t *testing.T) {
	env, _, _ := sagaEnv(t, "no id here\n")
	_, err := runSagaScript(t, "ask.sh", env) // no batch file in kv
	assert.Error(t, err)

	env, kv, _ := sagaEnv(t, "no id here\n")
	batch := filepath.Join(t.TempDir(), "batch.md")
	write(t, batch, "q1")
	write(t, filepath.Join(kv, "saga_batch_file"), batch)
	_, err = runSagaScript(t, "ask.sh", env) // no id in output
	assert.Error(t, err)
}

func TestSagaPoll(t *testing.T) {
	mk := func(statuses ...string) string {
		qs := make([]string, len(statuses))
		for i, s := range statuses {
			qs[i] = `{"id":"q","status":"` + s + `"}`
		}
		return `{"questions":[` + strings.Join(qs, ",") + `]}`
	}
	cases := []struct {
		name, out, marker string
	}{
		{"answered", mk("answered", "answered"), "CLOCHE_RESULT:answered"},
		{"reframe", mk("answered", "deprecated", "open"), "CLOCHE_RESULT:reframe"},
		{"pending", mk("answered", "open"), ""},
		{"held is pending", mk("held"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, kv, _ := sagaEnv(t, c.out)
			write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
			out, err := runSagaScript(t, "poll.sh", env)
			require.NoError(t, err, out)
			if c.marker == "" {
				assert.NotContains(t, out, "CLOCHE_RESULT")
			} else {
				assert.Contains(t, out, c.marker)
				assert.Equal(t, strings.TrimPrefix(c.marker, "CLOCHE_RESULT:"), kvRead(t, kv, "saga_outcome"))
			}
		})
	}

	env, kv, _ := sagaEnv(t, `{"questions":[]}`)
	write(t, filepath.Join(kv, "saga_batch_id"), "CLOCHE-B1")
	_, err := runSagaScript(t, "poll.sh", env)
	assert.Error(t, err, "empty batch must fail, not report answered")

	env, _, _ = sagaEnv(t, `{}`)
	_, err = runSagaScript(t, "poll.sh", env) // no batch id
	assert.Error(t, err)
}

func TestSagaJudgePrepare_OnlyOpenQuestions(t *testing.T) {
	env, kv, _ := sagaEnv(t, `{"questions":[{"id":"a","status":"open"},{"id":"b","status":"held"},{"id":"c","status":"answered"}]}`)
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
