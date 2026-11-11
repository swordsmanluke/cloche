package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkMainEnv builds a temp project (a git repo on main with one commit) and
// a bin dir holding stub `cloche` and `curl`. curl records its arguments in
// calls and fails when curl-fail exists.
func checkMainEnv(t *testing.T) (proj string, env []string) {
	t.Helper()
	root := t.TempDir()
	proj = filepath.Join(root, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o755))
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", proj}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte(".cloche/runs/\n"), 0o644))
	git("add", "a", ".gitignore")
	git("commit", "-q", "-m", "first")

	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "cloche"), []byte("#!/bin/sh\necho tok-123\n"), 0o755))
	curl := "#!/bin/sh\n[ -f \"" + root + "/curl-fail\" ] && exit 22\nfor a in \"$@\"; do printf 'ARG:%s\\n' \"$a\"; done >> \"" + root + "/calls\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0o755))

	env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"CLOCHE_PROJECT_DIR="+proj,
		"CLOCHE_SIGNAL_URL=http://daemon.test",
		"CLOCHE_SIGNAL_PROJECT=proj",
		"CLOCHE_PREV_OUTPUT=",
	)
	return proj, env
}

func runScript(t *testing.T, env []string, name string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join("..", "..", ".cloche", "scripts", name))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func headOf(t *testing.T, proj string) string {
	out, err := exec.Command("git", "-C", proj, "rev-parse", "main").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func TestCheckMain_WaitIsATimer(t *testing.T) {
	proj, env := checkMainEnv(t)

	// No target yet: due immediately.
	out, err := runScript(t, env, "check-main-wait.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:due")

	// Schedule puts the target in the future: pending (no marker).
	env = append(env, "CLOCHE_CHECK_MAIN_INTERVAL=3600")
	_, err = runScript(t, env, "check-main-schedule.sh")
	require.NoError(t, err)
	out, err = runScript(t, env, "check-main-wait.sh")
	require.NoError(t, err)
	assert.NotContains(t, out, "CLOCHE_RESULT")

	// A target in the past is due.
	state := filepath.Join(proj, ".cloche", "runs", "_check-main", "next_at")
	require.NoError(t, os.WriteFile(state, []byte(strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)), 0o644))
	out, err = runScript(t, env, "check-main-wait.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:due")
}

func TestCheckMain_SkipsWhenHeadHasNotMoved(t *testing.T) {
	_, env := checkMainEnv(t)

	out, err := runScript(t, env, "check-main-changed.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:changed")

	_, err = runScript(t, env, "check-main-record-green.sh")
	require.NoError(t, err)

	out, err = runScript(t, env, "check-main-changed.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:unchanged")
}

func TestCheckMain_SkipsWhenNotOnMain(t *testing.T) {
	proj, env := checkMainEnv(t)
	require.NoError(t, exec.Command("git", "-C", proj, "checkout", "-q", "-b", "feature").Run())
	out, err := runScript(t, env, "check-main-changed.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:skip")
}

func TestCheckMain_RedPostsUrgentSignal(t *testing.T) {
	proj, env := checkMainEnv(t)
	root := filepath.Dir(proj)

	// A green check, then two more commits, then red.
	_, err := runScript(t, env, "check-main-record-green.sh")
	require.NoError(t, err)
	green := headOf(t, proj)
	for _, m := range []string{"second", "third"} {
		cmd := exec.Command("git", "-C", proj, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", m)
		out, cerr := cmd.CombinedOutput()
		require.NoError(t, cerr, string(out))
	}

	prev := filepath.Join(root, "prev")
	require.NoError(t, os.WriteFile(prev, []byte("ok  \tpkg/a\n--- FAIL: TestRunner (0.1s)\nFAIL\tinternal/host\t0.2s\n"), 0o644))
	env = append(env, "CLOCHE_PREV_OUTPUT="+prev)

	out, err := runScript(t, env, "check-main-report-red.sh")
	require.NoError(t, err, out)

	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	require.NoError(t, err)
	c := string(calls)
	assert.Contains(t, c, "ARG:http://daemon.test/api/projects/proj/signals")
	assert.Contains(t, c, "ARG:Authorization: Bearer tok-123")
	assert.Contains(t, c, `"source": "ci-main"`)
	assert.Contains(t, c, `"key": "main-red"`)
	assert.Contains(t, c, `"severity": "urgent"`)
	assert.Contains(t, c, "--- FAIL: TestRunner")
	assert.Contains(t, c, green+"..") // range starts at the last green commit
	assert.Contains(t, c, "third")
	assert.NotContains(t, c, " first\n") // the green commit itself is outside the range

	// HEAD was recorded as checked, so it isn't re-checked until it moves.
	out, err = runScript(t, env, "check-main-changed.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:unchanged")
}

func TestCheckMain_FailedPostIsRetried(t *testing.T) {
	proj, env := checkMainEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(proj), "curl-fail"), nil, 0o644))

	_, err := runScript(t, env, "check-main-report-red.sh")
	require.Error(t, err)

	out, err := runScript(t, env, "check-main-changed.sh")
	require.NoError(t, err)
	assert.Contains(t, out, "CLOCHE_RESULT:changed")
}
