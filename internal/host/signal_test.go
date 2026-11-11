package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// stubCloche is a `cloche` that answers get/set from files in $STUB_DIR/kv and
// records `tasks` invocations (one ARG: line per argument) in $STUB_DIR/calls.
// `tasks find` reports $STUB_DIR/open's content as the found task, or "no open
// task" when that file is absent.
const stubCloche = `#!/bin/sh
d="$STUB_DIR"
case "$1" in
  get) [ -f "$d/kv/$2" ] || exit 1; cat "$d/kv/$2" ;;
  set) printf %s "$3" > "$d/kv/$2" ;;
  tasks)
    shift
    { for a in "$@"; do printf 'ARG:%s\n' "$a"; done; echo ---; } >> "$d/calls"
    case "$1" in
      find)
        if [ -f "$d/open" ]; then cat "$d/open"; exit 0; fi
        echo "no open task for that source and key" >&2; exit 1 ;;
      create) echo task-new ;;
      update) echo ok ;;
      *) exit 2 ;;
    esac ;;
  *) exit 2 ;;
esac
`

func runDefaultHandler(t *testing.T, sig signal.Signal, openTask string) (calls string, err error) {
	t.Helper()
	stub := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(stub, "kv"), 0o755))
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "cloche"), []byte(stubCloche), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_DIR", stub)
	if openTask != "" {
		require.NoError(t, os.WriteFile(filepath.Join(stub, "open"), []byte(openTask), 0o644))
	}
	for k, v := range signalKV(sig, 3) {
		if v != "" {
			require.NoError(t, os.WriteFile(filepath.Join(stub, "kv", k), []byte(v), 0o644))
		}
	}

	// No project defines handle-notification, so the built-in default runs.
	err = RunSignalHandler(context.Background(), &Runner{Store: newSQLiteStore(t)}, t.TempDir(), sig, nil)
	data, _ := os.ReadFile(filepath.Join(stub, "calls"))
	return string(data), err
}

func TestDefaultHandleNotification_NoOpenTaskCreates(t *testing.T) {
	sig := signal.Signal{Source: "ci-main", Key: "main-red", Severity: "urgent",
		Title: "main is red", Evidence: "FAIL internal/host\nexit 1", Domain: "ci"}
	calls, err := runDefaultHandler(t, sig, "")
	require.NoError(t, err)

	// find, then create — and nothing else.
	require.Equal(t, 2, strings.Count(calls, "---"), calls)
	findCall, createCall, _ := strings.Cut(calls, "---\n")
	assert.Contains(t, findCall, "ARG:find")
	assert.Contains(t, findCall, "ARG:ci-main")
	assert.Contains(t, findCall, "ARG:main-red")
	assert.Contains(t, createCall, "ARG:create")
	assert.Contains(t, createCall, "ARG:main is red")        // title as-is
	assert.Contains(t, createCall, "ARG:FAIL internal/host") // evidence as-is (first line)
	assert.Contains(t, createCall, "ARG:0")                  // urgent -> priority 0
	assert.Contains(t, createCall, "ARG:ci")                 // domain as a label
	assert.Contains(t, createCall, "ARG:--label")
	assert.NotContains(t, createCall, "ARG:update")
}

func TestDefaultHandleNotification_NoDomainNoLabel(t *testing.T) {
	sig := signal.Signal{Source: "s", Key: "k", Severity: "low", Title: "t"}
	calls, err := runDefaultHandler(t, sig, "")
	require.NoError(t, err)
	assert.NotContains(t, calls, "--label")
	assert.NotContains(t, calls, "--body")
	assert.Contains(t, calls, "ARG:3") // low -> priority 3
}

func TestDefaultHandleNotification_OpenTaskGetsComment(t *testing.T) {
	sig := signal.Signal{Source: "ci-main", Key: "main-red", Severity: "high",
		Title: "main is red", Evidence: "second failure"}
	calls, err := runDefaultHandler(t, sig, "task-7")
	require.NoError(t, err)

	require.Equal(t, 2, strings.Count(calls, "---"), calls)
	_, updateCall, _ := strings.Cut(calls, "---\n")
	assert.Contains(t, updateCall, "ARG:update")
	assert.Contains(t, updateCall, "ARG:task-7")
	assert.Contains(t, updateCall, "ARG:--comment")
	assert.Contains(t, updateCall, "Occurrences in this burst: 3")
	assert.Contains(t, updateCall, "second failure")
	assert.NotContains(t, calls, "ARG:create")
}

func TestSignalKV(t *testing.T) {
	kv := signalKV(signal.Signal{Source: "s", Key: "k", Severity: "high", Title: "T", Evidence: "E", Domain: "d"}, 1)
	assert.Equal(t, "s", kv["signal_source"])
	assert.Equal(t, "1", kv["signal_priority"])
	assert.Equal(t, "1", kv["signal_count"])
	assert.Contains(t, kv["signal_json"], `"key":"k"`)
}

// A handler that fails leaves a failed run behind, so the lost signal shows in
// `cloche list` rather than only in the daemon log.
func TestRunSignalHandler_FailureIsRecorded(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "cloche"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	store, dir := newSQLiteStore(t), t.TempDir()
	sig := signal.Signal{Source: "ci", Key: "k", Severity: signal.SeverityLow, Title: "t"}
	require.Error(t, RunSignalHandler(context.Background(), &Runner{Store: store}, dir, sig, nil))

	var runs []*domain.Run
	for _, r := range store.fakeStore.runs {
		runs = append(runs, r)
	}
	require.Len(t, runs, 1)
	assert.Equal(t, signal.WorkflowName, runs[0].WorkflowName)
	assert.Equal(t, domain.RunStateFailed, runs[0].State)
}
