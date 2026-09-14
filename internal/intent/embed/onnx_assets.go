package embed

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// onnxDefaultModel is the design's proposed default: small enough to
// vendor-fetch invisibly, validated against the spike corpus.
const onnxDefaultModel = "all-MiniLM-L6-v2"

// onnxRuntimeVersion is the pinned onnxruntime release these checksums and
// the vendored github.com/yalue/onnxruntime_go C API headers correspond
// to. Bumping it means re-pinning every checksum in onnxRuntimeLibraries.
const onnxRuntimeVersion = "1.30.0"

const onnxDownloadTimeout = 5 * time.Minute

// onnxModelName returns the model to embed with: the CLOCHE_INTENT_MODEL
// override or the default. This env var stands in for the design's
// intent.model config key — the daemon's config-to-embedder wiring lands
// in a later slice, so this package (which has no dependency on
// internal/config to avoid an import cycle risk) reads the environment
// directly, matching ollama.go's CLOCHE_OLLAMA_* pattern.
func onnxModelName() string {
	if v := os.Getenv("CLOCHE_INTENT_MODEL"); v != "" {
		return v
	}
	return onnxDefaultModel
}

// onnxAssetFile is one checksum-pinned file to fetch into the cache.
type onnxAssetFile struct {
	destName string // file name written under the destination directory
	url      string
	sha256   string
}

// onnxModelManifest pins the files that make up a supported onnx
// embedding model. Only the default, all-MiniLM-L6-v2, is populated;
// requesting any other name is an "unsupported model" outcome that
// onnxProbe (onnx.go) treats the same as an unsupported platform — log a
// warning and let the chain fall through to ollama/keyword.
type onnxModelManifest struct {
	dimensions int
	model      onnxAssetFile
	vocab      onnxAssetFile
}

var onnxModelManifests = map[string]onnxModelManifest{
	onnxDefaultModel: {
		dimensions: 384,
		model: onnxAssetFile{
			destName: "model.onnx",
			url:      "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main/onnx/model.onnx",
			sha256:   "759c3cd2b7fe7e93933ad23c4c9181b7396442a2ed746ec7c1d46192c469c46e",
		},
		vocab: onnxAssetFile{
			destName: "vocab.txt",
			url:      "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main/vocab.txt",
			sha256:   "07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3",
		},
	},
}

// onnxRuntimeArchive is a platform's onnxruntime release archive: a
// checksum-pinned .tgz plus the path of the shared-library member inside
// it worth extracting (the versioned .so/.dylib, not the unversioned
// symlink onnxruntime ships alongside it).
type onnxRuntimeArchive struct {
	onnxAssetFile
	libMember string // path within the archive to extract
	libName   string // file name to write the extracted library as
}

// onnxRuntimeLibraries covers the platforms release CI ships the onnx tag
// for first (linux/amd64, darwin/arm64); other GOOS/GOARCH combinations
// are an "unsupported platform" outcome (see onnxPlatformSupported).
var onnxRuntimeLibraries = map[string]onnxRuntimeArchive{
	"linux/amd64": {
		onnxAssetFile: onnxAssetFile{
			destName: "onnxruntime-linux-x64-" + onnxRuntimeVersion + ".tgz",
			url:      "https://github.com/microsoft/onnxruntime/releases/download/v" + onnxRuntimeVersion + "/onnxruntime-linux-x64-" + onnxRuntimeVersion + ".tgz",
			sha256:   "a5ed5a3cac51fbb2e90da632ae43d19212faaa20e76484e62bcb7c23ddb3b3fd",
		},
		libMember: "onnxruntime-linux-x64-" + onnxRuntimeVersion + "/lib/libonnxruntime.so." + onnxRuntimeVersion,
		libName:   "libonnxruntime.so." + onnxRuntimeVersion,
	},
	"darwin/arm64": {
		onnxAssetFile: onnxAssetFile{
			destName: "onnxruntime-osx-arm64-" + onnxRuntimeVersion + ".tgz",
			url:      "https://github.com/microsoft/onnxruntime/releases/download/v" + onnxRuntimeVersion + "/onnxruntime-osx-arm64-" + onnxRuntimeVersion + ".tgz",
			sha256:   "6ebb5062a934537c352937821f9fe9718e7de1a2db1122a93dd363ffd53a7012",
		},
		libMember: "onnxruntime-osx-arm64-" + onnxRuntimeVersion + "/lib/libonnxruntime." + onnxRuntimeVersion + ".dylib",
		libName:   "libonnxruntime." + onnxRuntimeVersion + ".dylib",
	},
}

// onnxPlatformSupported reports whether the running GOOS/GOARCH has a
// pinned onnxruntime shared library available to download.
func onnxPlatformSupported() bool {
	_, ok := onnxRuntimeLibraries[runtime.GOOS+"/"+runtime.GOARCH]
	return ok
}

// onnxCacheRoot resolves the first-use download destination,
// ~/.cache/cloche/models/, overridable via CLOCHE_MODEL_CACHE_DIR for
// tests and for users who want the cache elsewhere.
func onnxCacheRoot() (string, error) {
	if v := os.Getenv("CLOCHE_MODEL_CACHE_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("intent: resolving home directory for model cache: %w", err)
	}
	return filepath.Join(home, ".cache", "cloche", "models"), nil
}

// ensureOnnxModelAssets downloads (or reuses an already-verified cached
// copy of) the model and vocab files for modelName, returning their
// on-disk paths and the model's embedding dimensionality. An unrecognized
// modelName is reported as an error so the caller can treat it as an
// "unsupported model" and fall through the adapter chain.
func ensureOnnxModelAssets(ctx context.Context, modelName string) (modelPath, vocabPath string, dims int, err error) {
	manifest, ok := onnxModelManifests[modelName]
	if !ok {
		return "", "", 0, fmt.Errorf("intent: onnx model %q is not in the pinned manifest", modelName)
	}

	root, err := onnxCacheRoot()
	if err != nil {
		return "", "", 0, err
	}
	dir := filepath.Join(root, modelName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", 0, fmt.Errorf("intent: creating model cache directory: %w", err)
	}

	modelPath, err = ensureDownloaded(ctx, dir, manifest.model)
	if err != nil {
		return "", "", 0, fmt.Errorf("intent: fetching onnx model: %w", err)
	}
	vocabPath, err = ensureDownloaded(ctx, dir, manifest.vocab)
	if err != nil {
		return "", "", 0, fmt.Errorf("intent: fetching onnx tokenizer vocab: %w", err)
	}
	return modelPath, vocabPath, manifest.dimensions, nil
}

// ensureOnnxRuntimeLibrary downloads (or reuses a cached copy of) the
// onnxruntime shared library for the current platform, returning its path.
func ensureOnnxRuntimeLibrary(ctx context.Context) (string, error) {
	archive, ok := onnxRuntimeLibraries[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("intent: no pinned onnxruntime library for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	root, err := onnxCacheRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "onnxruntime", onnxRuntimeVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("intent: creating onnxruntime cache directory: %w", err)
	}

	libPath := filepath.Join(dir, archive.libName)
	if verified(dir, archive.libName) && fileMatchesChecksum(libPath, "") {
		return libPath, nil
	}

	archivePath, err := ensureDownloaded(ctx, dir, archive.onnxAssetFile)
	if err != nil {
		return "", fmt.Errorf("intent: fetching onnxruntime library archive: %w", err)
	}
	if err := extractTarGzMember(archivePath, archive.libMember, libPath); err != nil {
		return "", fmt.Errorf("intent: extracting onnxruntime library: %w", err)
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("intent: removing downloaded onnxruntime archive: %w", err)
	}
	if err := markVerified(dir, archive.libName); err != nil {
		return "", err
	}
	return libPath, nil
}

// ensureDownloaded returns the path to asset.destName under dir, fetching
// it first if absent or if the existing file's content doesn't match the
// pinned checksum. A checksum mismatch on an existing file is treated as
// corruption: the file is re-downloaded rather than trusted.
func ensureDownloaded(ctx context.Context, dir string, asset onnxAssetFile) (string, error) {
	dest := filepath.Join(dir, asset.destName)
	if fileMatchesChecksum(dest, asset.sha256) {
		return dest, nil
	}
	if err := downloadAndVerify(ctx, asset.url, dest, asset.sha256); err != nil {
		return "", err
	}
	return dest, nil
}

// downloadAndVerify streams url to a temp file alongside dest, verifies
// its SHA-256 against wantSHA256, and only then renames it into place —
// so a failed or corrupt download never leaves a bad file at dest, and a
// crash mid-download leaves only an orphaned temp file rather than a
// silently-truncated "real" one.
func downloadAndVerify(ctx context.Context, url, dest, wantSHA256 string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, onnxDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building download request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: unexpected status %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", dest, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", dest, err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA256 {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, wantSHA256)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("installing %s: %w", dest, err)
	}
	return nil
}

// fileMatchesChecksum reports whether dest exists and its content hashes
// to wantSHA256 (an empty wantSHA256 only checks existence).
func fileMatchesChecksum(dest, wantSHA256 string) bool {
	f, err := os.Open(dest)
	if err != nil {
		return false
	}
	defer f.Close()
	if wantSHA256 == "" {
		return true
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == wantSHA256
}

// verified/markVerified record that a file with no standalone checksum of
// its own (the extracted shared library — its integrity comes from the
// archive checksum verified at download time) has already been installed,
// so repeated probes don't re-download and re-extract the archive.
func verified(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name+".verified"))
	return err == nil
}

func markVerified(dir, name string) error {
	if err := os.WriteFile(filepath.Join(dir, name+".verified"), []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("recording extraction of %s: %w", name, err)
	}
	return nil
}

// extractTarGzMember extracts exactly one member from a .tar.gz archive
// (matched by suffix, to tolerate the leading "./" some archives — e.g.
// onnxruntime's osx tarballs — prefix entries with) to destPath.
func extractTarGzMember(archivePath, member, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("opening gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("member %q not found in archive", member)
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if name != member {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("creating destination directory: %w", err)
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("creating %s: %w", destPath, err)
		}
		defer out.Close()
		if _, err := io.Copy(out, tr); err != nil {
			return fmt.Errorf("writing %s: %w", destPath, err)
		}
		return nil
	}
}
