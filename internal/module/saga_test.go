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

// readRealShape runs read.sh against a saga stub that prints the given JSON
// verbatim for `saga show`, i.e. the shape real Saga emits (string author,
// possibly null status), not the older object-author stub shape.
func readRealShape(t *testing.T, showJSON string) (string, error) {
	b := newSagaBackend(t)
	fixture := filepath.Join(b.dir, "show.json")
	write(t, fixture, showJSON)
	write(t, filepath.Join(b.dir, "bin", "saga"), "#!/usr/bin/env bash\ncat "+fixture+"\n")
	require.NoError(t, os.Chmod(filepath.Join(b.dir, "bin", "saga"), 0o755))
	return b.Read("Q1")
}

func TestSagaModule_ReadAcceptsRealShapedAnswer(t *testing.T) {
	got, err := readRealShape(t, `{"id":"Q1","status":{"state":"in-force"},"answers":[`+
		`{"author":"human","select":[1],"status":null,"text":"use option one"}]}`)
	require.NoError(t, err)
	assert.Equal(t, "use option one\n", got)
}

func TestSagaModule_ReadPrefersHumanOverAgent(t *testing.T) {
	got, err := readRealShape(t, `{"id":"Q1","answers":[`+
		`{"author":"human","status":null,"text":"human says"},`+
		`{"author":"agent","status":null,"text":"agent says"}]}`)
	require.NoError(t, err)
	assert.Equal(t, "human says\n", got)
}

func TestSagaModule_ReadSkipsDeprecatedAndFailsWithoutAnswers(t *testing.T) {
	got, err := readRealShape(t, `{"id":"Q1","answers":[`+
		`{"author":"agent","status":null,"text":"first"},`+
		`{"author":"agent","status":null,"text":"later"},`+
		`{"author":"agent","status":{"state":"deprecated"},"text":"old"}]}`)
	require.NoError(t, err)
	assert.Equal(t, "later\n", got)

	_, err = readRealShape(t, `{"id":"Q1","answers":[]}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no answer")
}
