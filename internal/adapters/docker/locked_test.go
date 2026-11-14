package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/ports"
)

func TestApplyProfile_DRA(t *testing.T) {
	cfg := ports.ContainerConfig{Image: "proj:latest"}
	require.NoError(t, ApplyProfile(&cfg, "dra-perf", map[string]string{"container.profile": "dra"}))
	assert.Equal(t, DRAImage, cfg.Image)
	require.NotNil(t, cfg.Locked)
	assert.Equal(t, []string{".cloche/dras/dra-perf/notes.md"}, cfg.Locked.WritablePaths)
	assert.Contains(t, cfg.Locked.AllowHosts, "api.anthropic.com")
}

func TestApplyProfile_ExplicitImageAndNotes(t *testing.T) {
	cfg := ports.ContainerConfig{Image: "proj:latest"}
	require.NoError(t, ApplyProfile(&cfg, "w", map[string]string{
		"container.profile": "dra", "container.image": "my-dra:1", "container.notes": ".cloche/dras/x/notes.md",
	}))
	assert.Equal(t, "proj:latest", cfg.Image, "explicit image is chosen by the caller's resolution, not overridden")
	assert.Equal(t, []string{".cloche/dras/x/notes.md"}, cfg.Locked.WritablePaths)
}

func TestApplyProfile_NoProfileIsNoop(t *testing.T) {
	cfg := ports.ContainerConfig{Image: "proj:latest"}
	require.NoError(t, ApplyProfile(&cfg, "w", map[string]string{}))
	assert.Nil(t, cfg.Locked)
}

func TestApplyProfile_Rejects(t *testing.T) {
	for _, notes := range []string{"../x", "/etc/passwd", "a/../../b", ".", "a'b"} {
		cfg := ports.ContainerConfig{}
		err := ApplyProfile(&cfg, "w", map[string]string{"container.profile": "dra", "container.notes": notes})
		assert.Error(t, err, notes)
		assert.Nil(t, cfg.Locked)
	}
	err := ApplyProfile(&ports.ContainerConfig{}, "w", map[string]string{"container.profile": "typo"})
	assert.ErrorContains(t, err, "unknown container profile")
}

func TestDRAProfile_Forwards(t *testing.T) {
	t.Setenv("CLOCHE_ADDR", "127.0.0.1:50051")
	lc := DRAProfile(".cloche/dras/a/notes.md")
	assert.Equal(t, []string{"host.docker.internal:50051"}, lc.ForwardAddrs)
}

func TestLockedWrapperCmd(t *testing.T) {
	cmd := lockedWrapperCmd([]string{"cloche-agent"}, []string{".cloche/dras/a/notes.md"})
	assert.Contains(t, cmd, "chown root:root")
	assert.Contains(t, cmd, "chmod go-w,u-w")
	assert.Contains(t, cmd, "chown agent:agent '/workspace/.cloche/dras/a/notes.md'")
	assert.NotContains(t, cmd, "chown -R agent:agent /workspace ")
	assert.NotContains(t, cmd, "; exec")
	assert.Contains(t, cmd, " && exec gosu agent cloche-agent")
	assert.Contains(t, cmd, "|| true; }")
	assert.Contains(t, cmd, "lock-down could not be applied")
	// Only the .claude chown may be allowed to fail.
	assert.Equal(t, 1, strings.Count(cmd, "|| true"))
}

// A failing lock step must stop the script before gosu runs.
func TestLockedWrapperCmd_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "gosu.log")
	stub := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	stub("find", "exit 0")
	stub("mkdir", "exit 0")
	stub("touch", "exit 0")
	stub("chmod", "exit 0")
	stub("gosu", "echo called >> "+log)

	run := func(chownBody string) (string, error) {
		stub("chown", chownBody)
		cmd := exec.Command("/bin/sh", "-c", lockedWrapperCmd([]string{"cloche-agent"}, []string{"n.md"}))
		cmd.Env = []string{"PATH=" + dir}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("exit 1")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, lockdownFailedExitCode, exitErr.ExitCode())
	assert.Contains(t, out, "lock-down could not be applied")
	_, statErr := os.Stat(log)
	assert.True(t, os.IsNotExist(statErr), "gosu must not run when a lock step fails")

	// Only the /home/agent/.claude chown may fail.
	_, err = run(`case "$*" in *home/agent/.claude*) exit 1;; esac`)
	require.NoError(t, err)
	_, statErr = os.Stat(log)
	assert.NoError(t, statErr, "gosu runs when only the .claude chown fails")
}

func TestEgressCmd(t *testing.T) {
	argv, err := egressCmd(&ports.LockedConfig{
		AllowHosts:   []string{"api.anthropic.com"},
		ForwardAddrs: []string{"host.docker.internal:50051"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"cloche-agent", "egress", "--listen", ":3128",
		"--allow", "api.anthropic.com:443", "--forward", ":50051=host.docker.internal:50051"}, argv)
	_, err = egressCmd(&ports.LockedConfig{AllowHosts: []string{"*"}})
	assert.Error(t, err, "a wildcard-everything allowlist is refused")
}

func TestLockedNetworkArgs_NoPrivilegesNoDefaultRoute(t *testing.T) {
	args := strings.Join(lockedNetworkArgs("net1", "10.0.0.2"), " ")
	assert.Contains(t, args, "--network net1")
	assert.Contains(t, args, "host.docker.internal:10.0.0.2")
	assert.Contains(t, args, "HTTPS_PROXY=http://10.0.0.2:3128")
	assert.Contains(t, args, "no-new-privileges")
	assert.NotContains(t, args, "--privileged")
	assert.NotContains(t, args, "cap-add")
}

// The DRA image must not ship build tools, a tracker CLI or git (CLOCHE-A128).
func TestDRADockerfile_NoBuildTools(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docker", "cloche-dra", "Dockerfile"))
	require.NoError(t, err)
	// Only the final stage ships; the stages before the last FROM are builders.
	text := string(data)
	final := text[strings.LastIndex(text, "\nFROM "):]
	for _, banned := range []string{" golang", "gcc", "build-essential", " make", " git ", "/usr/local/go", " gh ", " bd ", "openssh", "id_ed25519", "gh_token"} {
		assert.NotContains(t, final, banned, "final DRA image stage must not include %q", banned)
	}
	for _, want := range []string{"cloche-agent", "/cloche ", "claude-code"} {
		assert.Contains(t, final, want)
	}
	assert.NotContains(t, strings.ToLower(text), "saga", "the DRA image ships no saga binary")
}

func draImageOrSkip(t *testing.T) {
	t.Helper()
	skipDockerIfUnavailable(t)
	if err := exec.Command("docker", "image", "inspect", DRAImage).Run(); err != nil {
		t.Skip(DRAImage + " not built; run `make docker-dra`")
	}
}

func TestDRAImage_HasNoBuildTools(t *testing.T) {
	draImageOrSkip(t)
	for _, tool := range []string{"go", "gcc", "cc", "make", "git", "bd", "gh"} {
		out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", DRAImage, "-c", "command -v "+tool).CombinedOutput()
		assert.Error(t, err, "%s must not be in the DRA image (found: %s)", tool, out)
	}
	out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", DRAImage, "-c", "command -v saga").CombinedOutput()
	assert.Error(t, err, "saga must not be in the DRA image (found: %s)", out)
	for _, tool := range []string{"cloche-agent", "cloche", "clo", "claude"} {
		out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", DRAImage, "-c", "command -v "+tool).CombinedOutput()
		assert.NoError(t, err, "%s must be in the DRA image: %s", tool, out)
	}
}

func TestLockedContainer_ProjectWriteFails(t *testing.T) {
	draImageOrSkip(t)
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("x"), 0o644))
	rt, err := NewRuntime()
	require.NoError(t, err)
	ctx := context.Background()
	run := func(id string, cmd ...string) int {
		cid, err := rt.Start(ctx, ports.ContainerConfig{
			Image: DRAImage, RunID: id, ProjectDir: proj, Cmd: cmd,
			Locked: &ports.LockedConfig{WritablePaths: []string{"notes.md"}},
		})
		require.NoError(t, err)
		defer rt.Remove(ctx, cid)
		code, err := rt.Wait(ctx, cid)
		require.NoError(t, err)
		return code
	}
	assert.NotEqual(t, 0, run("lock-w1", "touch", "/workspace/CLAUDE.md"), "project file write must fail")
	assert.NotEqual(t, 0, run("lock-w2", "touch", "/workspace/new-file"), "new project file must fail")
	assert.Equal(t, 0, run("lock-w3", "touch", "/workspace/notes.md"), "notes.md is writable")
}

// fakeDockerLog installs a fake docker that logs argv and reports an egress
// network label for inspect.
func fakeDockerLog(t *testing.T) string {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1\" in inspect) echo cloche-dra-x;; esac\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// Stopping a locked container (the end of a failed or kept run) removes the
// sidecar but not the main container or the network.
func TestStop_RemovesSidecarKeepsContainerAndNetwork(t *testing.T) {
	log := fakeDockerLog(t)
	require.NoError(t, (&Runtime{}).Stop(context.Background(), "main1"))
	data, _ := os.ReadFile(log)
	calls := string(data)
	assert.Contains(t, calls, "stop main1")
	assert.Contains(t, calls, "rm -f cloche-dra-x-egress")
	assert.NotContains(t, calls, "rm -f main1")
	assert.NotContains(t, calls, "network rm")
}

// Remove still removes the network, and tolerates the sidecar being gone.
func TestRemove_RemovesNetworkAfterSidecarGone(t *testing.T) {
	log := fakeDockerLog(t)
	require.NoError(t, (&Runtime{}).Remove(context.Background(), "main1"))
	data, _ := os.ReadFile(log)
	assert.Contains(t, string(data), "network rm cloche-dra-x")
	assert.Contains(t, string(data), "rm -f main1")
}
