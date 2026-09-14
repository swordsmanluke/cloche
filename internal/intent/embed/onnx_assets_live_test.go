package embed

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive_EnsureOnnxModelAssets_RealDownloadMatchesPinnedChecksum exercises
// the actual first-use download path against the real Hugging Face and
// onnxruntime release URLs baked into onnx_assets.go. It downloads ~90MB
// and is network-dependent, so — like the ollama regression fixture in
// regression_test.go — it only runs when explicitly opted into, never as
// part of the default `go test ./...`.
func TestLive_EnsureOnnxModelAssets_RealDownloadMatchesPinnedChecksum(t *testing.T) {
	if os.Getenv("CLOCHE_TEST_ONNX_LIVE") == "" {
		t.Skip("set CLOCHE_TEST_ONNX_LIVE=1 to exercise the real model/tokenizer download (~90MB, network required)")
	}
	t.Setenv("CLOCHE_MODEL_CACHE_DIR", t.TempDir())

	modelPath, vocabPath, dims, err := ensureOnnxModelAssets(context.Background(), onnxDefaultModel)
	require.NoError(t, err)
	assert.Equal(t, 384, dims)

	tok, err := newBertTokenizer(vocabPath)
	require.NoError(t, err)
	ids := tok.encode("the quick brown fox", 32)
	assert.NotEmpty(t, ids)

	info, err := os.Stat(modelPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(50_000_000)) // ~90MB fp32 export
}

// TestLive_EnsureOnnxRuntimeLibrary_RealDownloadMatchesPinnedChecksum is the
// same opt-in guard, covering the onnxruntime shared-library archive fetch
// and extraction rather than the model files.
func TestLive_EnsureOnnxRuntimeLibrary_RealDownloadMatchesPinnedChecksum(t *testing.T) {
	if os.Getenv("CLOCHE_TEST_ONNX_LIVE") == "" {
		t.Skip("set CLOCHE_TEST_ONNX_LIVE=1 to exercise the real onnxruntime library download (network required)")
	}
	if !onnxPlatformSupported() {
		t.Skipf("no pinned onnxruntime library for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	t.Setenv("CLOCHE_MODEL_CACHE_DIR", t.TempDir())

	libPath, err := ensureOnnxRuntimeLibrary(context.Background())
	require.NoError(t, err)

	info, err := os.Stat(libPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(1_000_000))
}
