package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubOverridesManifest replaces readContainerFile for the test duration. A
// nil manifest simulates a container without one.
func stubOverridesManifest(t *testing.T, m *overridesManifest) {
	t.Helper()
	orig := readContainerFile
	readContainerFile = func(_ context.Context, _, p string) ([]byte, error) {
		if p != overridesManifestPath {
			t.Fatalf("unexpected container read of %s", p)
		}
		if m == nil {
			return nil, fmt.Errorf("not there: %w", fs.ErrNotExist)
		}
		return json.Marshal(m)
	}
	t.Cleanup(func() { readContainerFile = orig })
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

func TestBuildOverridesManifest(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"CLAUDE.md":             "container rules\n",
		".claude/settings.json": `{"enabledPlugins":{"p@m":true}}`,
	})
	if err := os.Symlink("CLAUDE.md", filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}

	m, err := buildOverridesManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"CLAUDE.md":             digestOf("container rules\n"),
		".claude/settings.json": digestOf(`{"enabledPlugins":{"p@m":true}}`),
	}
	if m.Version != overridesManifestVersion || len(m.Files) != len(want) {
		t.Fatalf("manifest = %+v, want version %d and files %v", m, overridesManifestVersion, want)
	}
	for p, d := range want {
		if m.Files[p] != d {
			t.Errorf("Files[%q] = %q, want %q", p, m.Files[p], d)
		}
	}
}

func TestBuildOverridesManifestMissingDir(t *testing.T) {
	m, err := buildOverridesManifest(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 0 || m.Files == nil {
		t.Fatalf("want empty, non-nil Files; got %#v", m.Files)
	}
}

func TestValidManifestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"CLAUDE.md": true, ".claude/settings.json": true,
		"": false, "/etc/passwd": false, "../x": false, "..": false, "a/../b": false, "a//b": false, "./a": false,
	} {
		if got := validManifestPath(p); got != want {
			t.Errorf("validManifestPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestExtractLeavesOverridesOutOfResult covers each way an override file can
// end up: untouched and tracked at base (restored), untouched and new
// (removed), and edited by the agent (kept).
func TestExtractLeavesOverridesOutOfResult(t *testing.T) {
	repoDir, _ := setupTestRepo(t)
	writeTree(t, repoDir, map[string]string{"CLAUDE.md": "host rules\n"})
	gitOut(t, repoDir, "add", ".")
	gitOut(t, repoDir, "commit", "-q", "-m", "add CLAUDE.md")
	baseSHA := strings.TrimSpace(gitOut(t, repoDir, "rev-parse", "HEAD"))

	container := t.TempDir()
	writeTree(t, container, map[string]string{
		"README.md":             "# Test\n",
		"CLAUDE.md":             "container rules\n",               // override, untouched
		".claude/settings.json": `{"enabledPlugins":{"p@m":true}}`, // override-created, untouched
		"NOTES.md":              "agent rewrote this\n",            // override-created, then edited
		"result.txt":            "agent output\n",
	})
	overrideDockerCp(t, container)
	overrideDockerExec(t, nil)
	stubOverridesManifest(t, &overridesManifest{Version: 1, Files: map[string]string{
		"CLAUDE.md":             digestOf("container rules\n"),
		".claude/settings.json": digestOf(`{"enabledPlugins":{"p@m":true}}`),
		"NOTES.md":              digestOf("override notes\n"),
		"gone.md":               digestOf("agent deleted me\n"),
	}})

	wt := prepareForTest(t, repoDir, baseSHA, "ovr-1")
	if _, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID: "fake", WorktreeDir: wt.Dir, Branch: wt.Branch, BaseSHA: baseSHA,
		RunID: "ovr-1", WorkflowName: "develop", Result: "succeeded",
	}); err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	changed := gitOut(t, wt.Dir, "diff", "--name-status", baseSHA, "HEAD")
	if strings.TrimSpace(changed) != "A\tNOTES.md\nA\tresult.txt" {
		t.Errorf("branch changes =\n%s\nwant only NOTES.md and result.txt added", changed)
	}
	if got, _ := os.ReadFile(filepath.Join(wt.Dir, "CLAUDE.md")); string(got) != "host rules\n" {
		t.Errorf("worktree CLAUDE.md = %q, want base content", got)
	}
	if _, err := os.Stat(filepath.Join(wt.Dir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("override-created settings.json should be removed from worktree, stat err = %v", err)
	}
	if out := gitOut(t, wt.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("worktree should be clean after extract, got:\n%s", out)
	}
}

func TestExtractOverridesUnderSubPath(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	container := t.TempDir()
	writeTree(t, container, map[string]string{
		"README.md": "# Test\n",
		"CLAUDE.md": "container rules\n",
	})
	overrideDockerCp(t, container)
	overrideDockerExec(t, nil)
	stubOverridesManifest(t, &overridesManifest{Version: 1, Files: map[string]string{
		"repos/app/CLAUDE.md": digestOf("container rules\n"),
		"CLAUDE.md":           digestOf("container rules\n"), // root-level: not this worktree's
	}})

	wt := prepareForTest(t, repoDir, baseSHA, "ovr-sub")
	if _, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID: "fake", WorktreeDir: wt.Dir, Branch: wt.Branch, BaseSHA: baseSHA,
		RunID: "ovr-sub", ContainerSubPath: "repos/app",
	}); err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}
	if changed := gitOut(t, wt.Dir, "diff", "--name-only", baseSHA, "HEAD"); changed != "" {
		t.Errorf("subpath override should be left out, branch changes:\n%s", changed)
	}
}

func TestExtractWithoutManifestKeepsOverrides(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	container := t.TempDir()
	writeTree(t, container, map[string]string{"README.md": "# Test\n", "CLAUDE.md": "container rules\n"})
	overrideDockerCp(t, container) // stubs a missing manifest
	overrideDockerExec(t, nil)

	wt := prepareForTest(t, repoDir, baseSHA, "ovr-legacy")
	if _, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID: "fake", WorktreeDir: wt.Dir, Branch: wt.Branch, BaseSHA: baseSHA, RunID: "ovr-legacy",
	}); err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}
	if changed := strings.TrimSpace(gitOut(t, wt.Dir, "diff", "--name-only", baseSHA, "HEAD")); changed != "CLAUDE.md" {
		t.Errorf("branch changes = %q, want CLAUDE.md (pre-manifest behaviour)", changed)
	}
}

func TestExtractFailsOnBadManifest(t *testing.T) {
	for name, m := range map[string]*overridesManifest{
		"version": {Version: 99, Files: map[string]string{}},
		"path":    {Version: 1, Files: map[string]string{"../escape": digestOf("x")}},
	} {
		t.Run(name, func(t *testing.T) {
			repoDir, baseSHA := setupTestRepo(t)
			overrideDockerCp(t, makeFixtureDir(t))
			overrideDockerExec(t, nil)
			stubOverridesManifest(t, m)
			wt := prepareForTest(t, repoDir, baseSHA, "ovr-bad-"+name)
			_, err := ExtractResults(context.Background(), ExtractOptions{
				ContainerID: "fake", WorktreeDir: wt.Dir, Branch: wt.Branch, BaseSHA: baseSHA, RunID: "bad",
			})
			if err == nil || !strings.Contains(err.Error(), "overrides manifest") {
				t.Fatalf("want overrides manifest error, got %v", err)
			}
		})
	}
}
