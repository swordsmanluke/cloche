package docker

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectTarEntries reads a tar archive and returns a map from entry name to
// content (empty string for non-regular files) and a set of entry names.
func collectTarEntries(t *testing.T, r io.Reader) (map[string]string, map[string]byte) {
	t.Helper()
	files := map[string]string{}
	types := map[string]byte{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		types[hdr.Name] = hdr.Typeflag
		if hdr.Typeflag == tar.TypeReg {
			data, _ := io.ReadAll(tr)
			files[hdr.Name] = string(data)
		}
	}
	return files, types
}

func TestWriteTarFromProject_ExternalDirSymlink(t *testing.T) {
	// Create an external directory with content.
	extDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "file.txt"), []byte("external content"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(extDir, "sub"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "sub", "nested.txt"), []byte("nested"), 0644))

	// Create project directory with a symlink pointing outside.
	projDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "regular.txt"), []byte("regular"), 0644))
	require.NoError(t, os.Symlink(extDir, filepath.Join(projDir, "extlink")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Use a dummy ignore pattern so the tar path is exercised.
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	files, types := collectTarEntries(t, &buf)

	// Regular file must be present.
	assert.Equal(t, "regular", files["regular.txt"])

	// The external directory itself must appear as a directory entry.
	assert.Equal(t, byte(tar.TypeDir), types["extlink"])

	// Files inside the external directory must be inlined as regular files,
	// not as symlink entries that Docker's tarslip protection would drop.
	assert.Equal(t, byte(tar.TypeReg), types["extlink/file.txt"])
	assert.Equal(t, "external content", files["extlink/file.txt"])
	assert.Equal(t, byte(tar.TypeReg), types["extlink/sub/nested.txt"])
	assert.Equal(t, "nested", files["extlink/sub/nested.txt"])
}

func TestWriteTarFromProject_ExternalFileSymlink(t *testing.T) {
	// Create an external file.
	extDir := t.TempDir()
	extFile := filepath.Join(extDir, "target.txt")
	require.NoError(t, os.WriteFile(extFile, []byte("file content"), 0644))

	// Create project directory with a symlink to a file outside.
	projDir := t.TempDir()
	require.NoError(t, os.Symlink(extFile, filepath.Join(projDir, "filelink.txt")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	files, types := collectTarEntries(t, &buf)

	// The symlink to an external file must be inlined as a regular file.
	assert.Equal(t, byte(tar.TypeReg), types["filelink.txt"])
	assert.Equal(t, "file content", files["filelink.txt"])
}

func TestWriteTarFromProject_InternalSymlinkKept(t *testing.T) {
	// Create project directory with a symlink pointing inside the project.
	projDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "real.txt"), []byte("real"), 0644))
	// Relative symlink within the tree.
	require.NoError(t, os.Symlink("real.txt", filepath.Join(projDir, "alias.txt")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	_, types := collectTarEntries(t, &buf)

	// Internal symlinks must remain as symlink entries (not inlined).
	assert.Equal(t, byte(tar.TypeSymlink), types["alias.txt"])
	assert.Equal(t, byte(tar.TypeReg), types["real.txt"])
}

func TestWriteTarFromProject_BrokenExternalSymlinkSkipped(t *testing.T) {
	// Create project directory with a symlink to a non-existent external path.
	projDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "ok.txt"), []byte("ok"), 0644))
	// Symlink to an absolute path that doesn't exist.
	require.NoError(t, os.Symlink("/nonexistent/path/outside", filepath.Join(projDir, "broken")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	// EvalSymlinks fails for broken targets, so addDereferencedEntry is never
	// reached; the symlink is written as a regular symlink entry without error.
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	files, _ := collectTarEntries(t, &buf)

	// The regular file must still be present.
	assert.Equal(t, "ok", files["ok.txt"])
	// The broken symlink is not a regular file entry.
	_, hasBroken := files["broken"]
	assert.False(t, hasBroken)
}

func TestWriteTarFromProject_NestedExternalSymlinkInlined(t *testing.T) {
	// ext2 is a second external location referenced by a symlink inside ext1.
	ext2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ext2, "deep.txt"), []byte("deep content"), 0644))

	// ext1 is the external directory that the project symlinks to.
	// It contains a nested symlink that escapes ext1 and points into ext2.
	ext1 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ext1, "local.txt"), []byte("local"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(ext2, "deep.txt"), filepath.Join(ext1, "nestedlink.txt")))

	projDir := t.TempDir()
	require.NoError(t, os.Symlink(ext1, filepath.Join(projDir, "extlink")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	files, types := collectTarEntries(t, &buf)

	// The file inside ext1 must be present as a regular file.
	assert.Equal(t, byte(tar.TypeReg), types["extlink/local.txt"])
	assert.Equal(t, "local", files["extlink/local.txt"])

	// The nested external symlink must be inlined as a regular file,
	// not emitted as a symlink entry that Docker's tarslip would drop.
	assert.Equal(t, byte(tar.TypeReg), types["extlink/nestedlink.txt"])
	assert.Equal(t, "deep content", files["extlink/nestedlink.txt"])
}

func TestWriteTarFromProject_NestedExternalDirSymlinkInlined(t *testing.T) {
	// ext2 is a second external directory referenced by a symlink inside ext1.
	ext2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ext2, "file.txt"), []byte("ext2 content"), 0644))

	// ext1 contains a symlink to a directory outside ext1.
	ext1 := t.TempDir()
	require.NoError(t, os.Symlink(ext2, filepath.Join(ext1, "dirlink")))

	projDir := t.TempDir()
	require.NoError(t, os.Symlink(ext1, filepath.Join(projDir, "extlink")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	files, types := collectTarEntries(t, &buf)

	// The nested external directory symlink must be inlined as a directory
	// with its contents as regular files.
	assert.Equal(t, byte(tar.TypeDir), types["extlink/dirlink"])
	assert.Equal(t, byte(tar.TypeReg), types["extlink/dirlink/file.txt"])
	assert.Equal(t, "ext2 content", files["extlink/dirlink/file.txt"])
}

func TestWriteTarFromProject_NestedInternalSymlinkKept(t *testing.T) {
	// ext1 contains a symlink that points to a sibling within ext1 (internal).
	ext1 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ext1, "real.txt"), []byte("real"), 0644))
	// Relative symlink within ext1: should be kept as a symlink entry.
	require.NoError(t, os.Symlink("real.txt", filepath.Join(ext1, "alias.txt")))

	projDir := t.TempDir()
	require.NoError(t, os.Symlink(ext1, filepath.Join(projDir, "extlink")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())

	_, types := collectTarEntries(t, &buf)

	// The real file must be present.
	assert.Equal(t, byte(tar.TypeReg), types["extlink/real.txt"])
	// The relative symlink within ext1 is internal and must remain a symlink.
	assert.Equal(t, byte(tar.TypeSymlink), types["extlink/alias.txt"])
}

func TestAddDereferencedEntry_InaccessibleTargetErrors(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Pass a non-existent resolved target: os.Stat inside addDereferencedEntry
	// must propagate the error instead of swallowing it.
	err := addDereferencedEntry(tw, "/nonexistent/cloche-test-target", "link")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link")
}

func TestIsInsideDir(t *testing.T) {
	cases := []struct {
		path   string
		dir    string
		inside bool
	}{
		{"/a/b", "/a/b", true},   // equal
		{"/a/b/c", "/a/b", true}, // child
		{"/a/b", "/a/bc", false}, // sibling with shared prefix
		{"/a", "/a/b", false},    // parent
		{"/a/bc", "/a/b", false}, // sibling
	}
	for _, tc := range cases {
		got := isInsideDir(tc.path, tc.dir)
		assert.Equal(t, tc.inside, got, "isInsideDir(%q, %q)", tc.path, tc.dir)
	}
}
