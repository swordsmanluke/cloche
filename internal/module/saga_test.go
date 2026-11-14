package module_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/module/qaconformance"
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

// stubSaga is a fake saga binary for the qa backend module. State lives in
// $SAGA_STUB_DIR (one file per question); `saga answer` follows the stub's own
// shape: saga answer <id> human|judge <text>.
const stubSaga = `#!/usr/bin/env bash
set -euo pipefail
s="${SAGA_STUB_DIR:?}"; mkdir -p "$s"
echo "$@" >> "$s/log"
case "$1" in
  ask)
    shift; file=""
    while [ $# -gt 0 ]; do case "$1" in --file) file="$2"; shift ;; esac; shift; done
    n=$(jq '.questions | length' "$file"); ids=()
    for _ in $(seq "$n"); do
      c=$(( $(cat "$s/next" 2>/dev/null || echo 0) + 1 )); echo "$c" > "$s/next"
      echo '[]' > "$s/Q$c.answers"; ids+=("Q$c")
    done
    printf '%s\n' "${ids[@]}" | jq -R . | jq -sc '{batch:"B1",questions:.}' ;;
  show)
    jq -c --arg id "$2" '{id:$id,status:{state:(if length>0 then "in-force" else "open" end)},answers:.}' "$s/$2.answers" ;;
  answer)
    jq -c --arg k "$3" --arg t "$4" '. + [{author:{kind:$k},text:$t}]' "$s/$2.answers" > "$s/$2.tmp" && mv "$s/$2.tmp" "$s/$2.answers" ;;
  *) echo "stub saga: unsupported: $*" >&2; exit 2 ;;
esac
`

const stubClocheKV = `#!/usr/bin/env bash
case "$1" in
  get) [ -e "$KV_DIR/$2" ] || { echo "kv: $2 not set" >&2; exit 1; }; cat "$KV_DIR/$2" ;;
  set) printf '%s' "$3" > "$KV_DIR/$2" ;;
  *) exit 2 ;;
esac
`

type sagaBackend struct {
	t   *testing.T
	dir string
}

func newSagaBackend(t *testing.T) *sagaBackend {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	write(t, filepath.Join(bin, "saga"), stubSaga)
	write(t, filepath.Join(bin, "cloche"), stubClocheKV)
	require.NoError(t, os.Chmod(filepath.Join(bin, "saga"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(bin, "cloche"), 0o755))
	return &sagaBackend{t: t, dir: dir}
}

func sagaModuleDir(t *testing.T) string {
	mod, err := filepath.Abs(filepath.Join("..", "..", ".cloche", "modules", "saga"))
	require.NoError(t, err)
	return mod
}

func (b *sagaBackend) env() []string {
	mod := sagaModuleDir(b.t)
	return append(os.Environ(),
		"PATH="+filepath.Join(b.dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SAGA_STUB_DIR="+filepath.Join(b.dir, "state"), "KV_DIR="+filepath.Join(b.dir, "kv"),
		"CLOCHE_MODULE_DIR="+mod, "SAGA_PROJECT=cloche", "TMPDIR="+b.dir)
}

func (b *sagaBackend) phase(script string, in map[string]string) (map[string]string, error) {
	kv := filepath.Join(b.dir, "kv")
	os.RemoveAll(kv)
	require.NoError(b.t, os.MkdirAll(kv, 0o755))
	for k, v := range in {
		write(b.t, filepath.Join(kv, k), v)
	}
	cmd := exec.Command("bash", filepath.Join(sagaModuleDir(b.t), "scripts", script))
	cmd.Env = b.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", script, err, strings.TrimSpace(string(out)))
	}
	out := map[string]string{}
	entries, err := os.ReadDir(kv)
	require.NoError(b.t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(kv, e.Name()))
		require.NoError(b.t, err)
		out[e.Name()] = string(data)
	}
	return out, nil
}

func (b *sagaBackend) Ask(file string) ([]string, error) {
	// The conformance file has no backend.feature; add one like a real caller.
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	withFeature := filepath.Join(b.dir, "questions.json")
	write(b.t, withFeature, strings.TrimSuffix(strings.TrimSpace(string(data)), "}")+`,"backend":{"feature":"cloche/qa-backends"}}`)
	out, err := b.phase("ask.sh", map[string]string{"questions_file": withFeature})
	if err != nil {
		return nil, err
	}
	return strings.Fields(out["question_ids"]), nil
}

func (b *sagaBackend) Open(ids []string) ([]string, error) {
	out, err := b.phase("await.sh", map[string]string{"question_ids": strings.Join(ids, "\n")})
	if err != nil {
		return nil, err
	}
	return strings.Fields(out["questions_open"]), nil
}

func (b *sagaBackend) Read(id string) (string, error) {
	out, err := b.phase("read.sh", map[string]string{"question_id": id})
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(out["answer_path"])
	return string(data), err
}

func (b *sagaBackend) Answer(id, who, text string) error {
	cmd := exec.Command(filepath.Join(b.dir, "bin", "saga"), "answer", id, who, text)
	cmd.Env = b.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("saga answer: %v: %s", err, out)
	}
	return nil
}

func TestSagaModulePassesQAConformance(t *testing.T) {
	qaconformance.Run(t, func(t *testing.T) qaconformance.Backend { return newSagaBackend(t) })
}

func TestSagaModule_AskUsesBindProjectAndFileFeature(t *testing.T) {
	b := newSagaBackend(t)
	_, err := b.Ask(func() string {
		f := filepath.Join(t.TempDir(), "q.json")
		write(t, f, qaconformance.QuestionsFileBody)
		return f
	}())
	require.NoError(t, err)
	logged, err := os.ReadFile(filepath.Join(b.dir, "state", "log"))
	require.NoError(t, err)
	assert.Contains(t, string(logged), "ask -p cloche -f cloche/qa-backends --file ")
}

func TestSagaModule_FilesShip(t *testing.T) {
	dir := filepath.Join("..", "..", ".cloche", "modules", "saga")
	judge, err := os.ReadFile(filepath.Join(dir, "judge.md"))
	require.NoError(t, err)
	for _, want := range []string{"saga guide", "saga search --follow", "saga answer --judge", "saga hold"} {
		assert.Contains(t, string(judge), want)
	}
	_, err = os.Stat(filepath.Join(dir, "asking.md"))
	assert.NoError(t, err)
}
