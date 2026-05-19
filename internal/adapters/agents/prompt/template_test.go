package prompt_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKVReader is an in-memory KVReader for tests.
type fakeKVReader struct {
	store map[string]string
	err   error // if non-nil, all Get calls return this error
}

func (f *fakeKVReader) Get(_ context.Context, key string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.store[key]
	return v, ok, nil
}

func newResolver(t *testing.T, builtins map[string]string, kv map[string]string) *prompt.Resolver {
	t.Helper()
	return &prompt.Resolver{
		Builtins: builtins,
		KV:       &fakeKVReader{store: kv},
		WorkDir:  t.TempDir(),
	}
}

func TestResolver_PlainText_PassesThrough(t *testing.T) {
	r := newResolver(t, nil, nil)
	got, err := r.Resolve(context.Background(), "no directives here")
	require.NoError(t, err)
	assert.Equal(t, "no directives here", got)
}

// ─── {{ $var }} ───────────────────────────────────────────────────────────────

func TestResolver_BuiltinVar_Resolves(t *testing.T) {
	r := newResolver(t, map[string]string{"task_id": "abc-123"}, nil)
	got, err := r.Resolve(context.Background(), "Task is {{ $task_id }}")
	require.NoError(t, err)
	assert.Equal(t, "Task is abc-123", got)
}

func TestResolver_KVVar_Resolves(t *testing.T) {
	r := newResolver(t, nil, map[string]string{"artifact_path": "/tmp/out.tar.gz"})
	got, err := r.Resolve(context.Background(), "Read {{ $artifact_path }} for results")
	require.NoError(t, err)
	assert.Equal(t, "Read /tmp/out.tar.gz for results", got)
}

func TestResolver_BuiltinShadowsKV(t *testing.T) {
	r := newResolver(t,
		map[string]string{"task_id": "real-task-id"},
		map[string]string{"task_id": "kv-override"},
	)
	got, err := r.Resolve(context.Background(), "ID is {{ $task_id }}")
	require.NoError(t, err)
	assert.Equal(t, "ID is real-task-id", got)
	assert.NotContains(t, got, "kv-override")
}

func TestResolver_MissingVar_Errors(t *testing.T) {
	r := newResolver(t, nil, nil)
	_, err := r.Resolve(context.Background(), "{{ $no_such_var }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_such_var")
}

func TestResolver_NilKV_NonBuiltin_Errors(t *testing.T) {
	r := &prompt.Resolver{
		Builtins: map[string]string{"task_id": "x"},
		KV:       nil,
		WorkDir:  t.TempDir(),
	}
	_, err := r.Resolve(context.Background(), "{{ $missing }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestResolver_KVError_Propagates(t *testing.T) {
	r := &prompt.Resolver{
		Builtins: nil,
		KV:       &fakeKVReader{err: errors.New("kv down")},
		WorkDir:  t.TempDir(),
	}
	_, err := r.Resolve(context.Background(), "{{ $some_key }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kv down")
}

func TestResolver_WhitespaceInsideDirective_Tolerated(t *testing.T) {
	r := newResolver(t, map[string]string{"task_id": "xyz"}, nil)
	got, err := r.Resolve(context.Background(), "{{$task_id}}")
	require.NoError(t, err)
	assert.Equal(t, "xyz", got)
}

// ─── {{! cmd }} ───────────────────────────────────────────────────────────────

func TestResolver_ShellDirective_Resolves(t *testing.T) {
	r := newResolver(t, nil, nil)
	got, err := r.Resolve(context.Background(), "{{! echo greetings }}")
	require.NoError(t, err)
	assert.Equal(t, "greetings", got)
}

func TestResolver_ShellDirective_InnerVarResolvesFirst(t *testing.T) {
	r := newResolver(t, map[string]string{"step_name": "analyze"}, nil)
	got, err := r.Resolve(context.Background(), "{{! echo {{ $step_name }} }}")
	require.NoError(t, err)
	assert.Equal(t, "analyze", got)
}

func TestResolver_ShellDirective_DollarDollar_BecomesLiteralDollar(t *testing.T) {
	t.Setenv("CLOCHE_TEST_VAR_XYZ", "hello-cloche")
	r := newResolver(t, nil, nil)
	got, err := r.Resolve(context.Background(), "{{! echo $$CLOCHE_TEST_VAR_XYZ }}")
	require.NoError(t, err)
	assert.Equal(t, "hello-cloche", got)
}

func TestResolver_DollarDollar_OutsideShell_Untouched(t *testing.T) {
	r := newResolver(t, nil, nil)
	got, err := r.Resolve(context.Background(), "outside: $$FOOBAR")
	require.NoError(t, err)
	assert.Equal(t, "outside: $$FOOBAR", got)
}

func TestResolver_ShellDirective_NonZeroExit_Errors(t *testing.T) {
	r := newResolver(t, nil, nil)
	_, err := r.Resolve(context.Background(), "{{! exit 42 }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit")
}

func TestResolver_ShellDirective_Timeout_Errors(t *testing.T) {
	r := &prompt.Resolver{
		Builtins: nil,
		KV:       &fakeKVReader{store: nil},
		WorkDir:  t.TempDir(),
		Timeout:  50 * time.Millisecond,
	}
	_, err := r.Resolve(context.Background(), "{{! sleep 10 }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestResolver_ShellDirective_TrailingNewlineTrimmed(t *testing.T) {
	r := newResolver(t, nil, nil)
	got, err := r.Resolve(context.Background(), "{{! printf 'hello' }}")
	require.NoError(t, err)
	assert.Equal(t, "hello", got)
}

// ─── {{@ path }} ──────────────────────────────────────────────────────────────

func TestResolver_FileDirective_Resolves(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "context.txt"), []byte("file contents here"), 0644))
	r := &prompt.Resolver{WorkDir: dir, KV: &fakeKVReader{}}
	got, err := r.Resolve(context.Background(), "{{@ context.txt }}")
	require.NoError(t, err)
	assert.Equal(t, "file contents here", got)
}

func TestResolver_FileDirective_InnerVarInPathResolvesFirst(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "analyze.txt"), []byte("step context loaded"), 0644))
	r := &prompt.Resolver{
		Builtins: map[string]string{"step_name": "analyze"},
		WorkDir:  dir,
		KV:       &fakeKVReader{},
	}
	got, err := r.Resolve(context.Background(), "{{@ {{ $step_name }}.txt }}")
	require.NoError(t, err)
	assert.Equal(t, "step context loaded", got)
}

func TestResolver_FileDirective_MissingFile_Errors(t *testing.T) {
	r := newResolver(t, nil, nil)
	_, err := r.Resolve(context.Background(), "{{@ missing_file.txt }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing_file.txt")
}

func TestResolver_FileContents_NotReTemplated(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "raw.txt"), []byte("{{ $task_id }}"), 0644))
	r := &prompt.Resolver{
		Builtins: map[string]string{"task_id": "should-not-appear"},
		WorkDir:  dir,
		KV:       &fakeKVReader{},
	}
	got, err := r.Resolve(context.Background(), "{{@ raw.txt }}")
	require.NoError(t, err)
	// Contents should be inserted literally, not re-resolved.
	assert.Equal(t, "{{ $task_id }}", got)
}

func TestResolver_ShellOutput_NotReTemplated(t *testing.T) {
	r := newResolver(t, nil, nil)
	// The shell echoes a {{ }} sequence; it must NOT be re-evaluated.
	got, err := r.Resolve(context.Background(), "{{! echo '{{ $task_id }}' }}")
	require.NoError(t, err)
	assert.Equal(t, "{{ $task_id }}", got)
}

// ─── Legacy LegacySubstitute ──────────────────────────────────────────────────

func TestLegacySubstitute_TaskDescription(t *testing.T) {
	var warnings []string
	got := prompt.LegacySubstitute(
		"{task_description} is the goal", "implement the feature", "", func(p string) {
			warnings = append(warnings, p)
		},
	)
	assert.Equal(t, "implement the feature is the goal", got)
	assert.Equal(t, []string{"{task_description}"}, warnings)
}

func TestLegacySubstitute_PreviousOutput(t *testing.T) {
	var warnings []string
	got := prompt.LegacySubstitute(
		"Prior: {previous_output}", "", "step 1 produced this", func(p string) {
			warnings = append(warnings, p)
		},
	)
	assert.Equal(t, "Prior: step 1 produced this", got)
	assert.Equal(t, []string{"{previous_output}"}, warnings)
}

func TestLegacySubstitute_WarnOncePerPattern(t *testing.T) {
	var warnings []string
	got := prompt.LegacySubstitute(
		"{task_description} and again {task_description}", "do the thing", "", func(p string) {
			warnings = append(warnings, p)
		},
	)
	assert.Equal(t, "do the thing and again do the thing", got)
	// warn called once, not twice
	count := 0
	for _, w := range warnings {
		if strings.Contains(w, "{task_description}") {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestLegacySubstitute_BothPatterns(t *testing.T) {
	var warnings []string
	got := prompt.LegacySubstitute(
		"{task_description}: {previous_output}", "implement the feature", "tests passed", func(p string) {
			warnings = append(warnings, p)
		},
	)
	assert.Equal(t, "implement the feature: tests passed", got)
	assert.Contains(t, warnings, "{task_description}")
	assert.Contains(t, warnings, "{previous_output}")
	assert.Len(t, warnings, 2)
}

func TestLegacySubstitute_NoMatch_NoWarning(t *testing.T) {
	var warnings []string
	got := prompt.LegacySubstitute("plain text", "desc", "out", func(p string) {
		warnings = append(warnings, p)
	})
	assert.Equal(t, "plain text", got)
	assert.Empty(t, warnings)
}
