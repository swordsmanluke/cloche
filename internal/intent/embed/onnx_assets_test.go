package embed

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestDownloadAndVerify_WritesFileWhenChecksumMatches(t *testing.T) {
	body := []byte("pretend model bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := downloadAndVerify(context.Background(), srv.URL, dest, sha256Hex(body))
	require.NoError(t, err)

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestDownloadAndVerify_ChecksumMismatchLeavesNoFileBehind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("actual bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	err := downloadAndVerify(context.Background(), srv.URL, dest, "0000000000000000000000000000000000000000000000000000000000000000")
	require.Error(t, err)

	_, statErr := os.Stat(dest)
	assert.True(t, os.IsNotExist(statErr), "no file should be installed at dest after a checksum mismatch")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no leftover temp file should remain after a failed download")
}

func TestDownloadAndVerify_HTTPErrorStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := downloadAndVerify(context.Background(), srv.URL, dest, sha256Hex(nil))
	assert.Error(t, err)
}

func TestEnsureDownloaded_ReusesCachedFileMatchingChecksum(t *testing.T) {
	calls := 0
	body := []byte("cached content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	asset := onnxAssetFile{destName: "f.bin", url: srv.URL, sha256: sha256Hex(body)}

	_, err := ensureDownloaded(context.Background(), dir, asset)
	require.NoError(t, err)
	_, err = ensureDownloaded(context.Background(), dir, asset)
	require.NoError(t, err)

	assert.Equal(t, 1, calls, "second call should reuse the cached, checksum-verified file rather than re-downloading")
}

func TestEnsureDownloaded_RedownloadsWhenCachedFileIsCorrupt(t *testing.T) {
	calls := 0
	body := []byte("good content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "f.bin")
	require.NoError(t, os.WriteFile(dest, []byte("corrupted"), 0o644))

	asset := onnxAssetFile{destName: "f.bin", url: srv.URL, sha256: sha256Hex(body)}
	_, err := ensureDownloaded(context.Background(), dir, asset)
	require.NoError(t, err)

	assert.Equal(t, 1, calls)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestOnnxCacheRoot_RespectsEnvOverride(t *testing.T) {
	t.Setenv("CLOCHE_MODEL_CACHE_DIR", "/tmp/some-override-dir")
	root, err := onnxCacheRoot()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/some-override-dir", root)
}

func TestOnnxCacheRoot_DefaultsUnderHomeCache(t *testing.T) {
	t.Setenv("CLOCHE_MODEL_CACHE_DIR", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	root, err := onnxCacheRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".cache", "cloche", "models"), root)
}

func TestOnnxModelName_DefaultsToMiniLM(t *testing.T) {
	t.Setenv("CLOCHE_INTENT_MODEL", "")
	assert.Equal(t, onnxDefaultModel, onnxModelName())
}

func TestOnnxModelName_RespectsEnvOverride(t *testing.T) {
	t.Setenv("CLOCHE_INTENT_MODEL", "some-other-model")
	assert.Equal(t, "some-other-model", onnxModelName())
}

func TestEnsureOnnxModelAssets_UnknownModelIsAnError(t *testing.T) {
	t.Setenv("CLOCHE_MODEL_CACHE_DIR", t.TempDir())
	_, _, _, err := ensureOnnxModelAssets(context.Background(), "not-a-real-model")
	assert.Error(t, err)
}

func TestOnnxPlatformSupported_MatchesPinnedManifest(t *testing.T) {
	_, wantSupported := onnxRuntimeLibraries[runtime.GOOS+"/"+runtime.GOARCH]
	assert.Equal(t, wantSupported, onnxPlatformSupported())
}

func TestOnnxRuntimeLibraries_ShipsLinuxAmd64AndDarwinArm64First(t *testing.T) {
	_, ok := onnxRuntimeLibraries["linux/amd64"]
	assert.True(t, ok)
	_, ok = onnxRuntimeLibraries["darwin/arm64"]
	assert.True(t, ok)
}

// buildTestTarGz writes a minimal .tar.gz containing one member with the
// given name and content, prefixed to mimic how real release archives
// (including onnxruntime's, whose osx tarball entries carry a leading
// "./") lay out their contents.
func buildTestTarGz(t *testing.T, memberName string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.tgz")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: memberName,
		Mode: 0o755,
		Size: int64(len(content)),
	}))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return path
}

func TestExtractTarGzMember_ExtractsNamedMember(t *testing.T) {
	content := []byte("shared library bytes")
	archive := buildTestTarGz(t, "onnxruntime-1.0.0/lib/libonnxruntime.so.1.0.0", content)

	dest := filepath.Join(t.TempDir(), "libonnxruntime.so.1.0.0")
	err := extractTarGzMember(archive, "onnxruntime-1.0.0/lib/libonnxruntime.so.1.0.0", dest)
	require.NoError(t, err)

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestExtractTarGzMember_TrimsLeadingDotSlashPrefix(t *testing.T) {
	content := []byte("dylib bytes")
	archive := buildTestTarGz(t, "./onnxruntime-1.0.0/lib/libonnxruntime.1.0.0.dylib", content)

	dest := filepath.Join(t.TempDir(), "libonnxruntime.1.0.0.dylib")
	err := extractTarGzMember(archive, "onnxruntime-1.0.0/lib/libonnxruntime.1.0.0.dylib", dest)
	require.NoError(t, err)

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestExtractTarGzMember_MemberNotFoundIsAnError(t *testing.T) {
	archive := buildTestTarGz(t, "some/other/file", []byte("x"))
	dest := filepath.Join(t.TempDir(), "out")
	err := extractTarGzMember(archive, "does/not/exist", dest)
	assert.Error(t, err)
}

func TestFileMatchesChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	body := []byte("hello")
	require.NoError(t, os.WriteFile(path, body, 0o644))

	assert.True(t, fileMatchesChecksum(path, sha256Hex(body)))
	assert.False(t, fileMatchesChecksum(path, sha256Hex([]byte("different"))))
	assert.True(t, fileMatchesChecksum(path, "")) // existence-only check
	assert.False(t, fileMatchesChecksum(filepath.Join(t.TempDir(), "missing"), ""))
}
