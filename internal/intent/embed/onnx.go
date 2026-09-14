//go:build onnx

// Package embed's onnx adapter: in-process inference via ONNX Runtime,
// linked through github.com/yalue/onnxruntime_go (which dlopen's the
// shared library at runtime rather than link-time, but still requires
// cgo to build the wrapper itself — hence the build tag). Only cloched
// is built with -tags onnx; cloche, clo, and cloche-agent stay cgo-free
// because embedding is exclusively the daemon's job (see
// docs/plans/2026-09-13-intent-continuity-design.md, "Build isolation").
//
// The download/checksum/cache logic (onnx_assets.go) and the WordPiece
// tokenizer + mean-pooling math (onnx_tokenizer.go, onnx_pool.go) live in
// separate, non-tagged files with no onnxruntime dependency, so they
// compile and unit-test in every build, including this repo's default
// cgo-free one.
package embed

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	onnxInputIDsName      = "input_ids"
	onnxAttentionMaskName = "attention_mask"
	onnxTokenTypeIDsName  = "token_type_ids"
	onnxOutputHiddenName  = "last_hidden_state"

	// onnxMaxSeqLen bounds tokenized input length. Requirement text plus
	// hints and query text (task description + prompt/workflow name, per
	// the design's "what gets embedded") comfortably fits well under this;
	// the model itself supports up to 512.
	onnxMaxSeqLen = 256
)

func init() {
	Register("onnx", onnxAvailable, onnxBuild)
}

// onnxReady memoizes the first-use download + onnxruntime environment
// initialization for the process lifetime: onnxAvailable (the chain
// probe) does the actual work, so that a successful probe guarantees
// onnxBuild can't fail for a reason the probe could have already caught.
var (
	onnxReadyOnce sync.Once
	onnxReadyErr  error
	onnxReady     struct {
		modelPath string
		vocabPath string
		dims      int
		modelName string
	}
)

// onnxAvailable is the chain probe. Any failure — unsupported platform,
// an intent.model name not in the pinned manifest, a network or checksum
// failure fetching assets, or a broken onnxruntime install — is logged
// once and reported as unavailable so Resolve falls through to
// ollama/keyword rather than blocking a run, per the design's "graceful
// chain fallback when unsupported" requirement.
func onnxAvailable(ctx context.Context) bool {
	onnxReadyOnce.Do(func() { onnxReadyErr = onnxInitialize(ctx) })
	if onnxReadyErr != nil {
		log.Printf("intent: onnx embedder unavailable, falling back to the next adapter in the chain: %v", onnxReadyErr)
		return false
	}
	return true
}

func onnxInitialize(ctx context.Context) error {
	if !onnxPlatformSupported() {
		return fmt.Errorf("no onnxruntime build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	modelName := onnxModelName()
	modelPath, vocabPath, dims, err := ensureOnnxModelAssets(ctx, modelName)
	if err != nil {
		return fmt.Errorf("fetching model %q: %w", modelName, err)
	}
	libPath, err := ensureOnnxRuntimeLibrary(ctx)
	if err != nil {
		return fmt.Errorf("fetching onnxruntime library: %w", err)
	}

	if !ort.IsInitialized() {
		ort.SetSharedLibraryPath(libPath)
		if err := ort.InitializeEnvironment(); err != nil {
			return fmt.Errorf("initializing onnxruntime environment: %w", err)
		}
	}

	onnxReady.modelPath = modelPath
	onnxReady.vocabPath = vocabPath
	onnxReady.dims = dims
	onnxReady.modelName = modelName
	return nil
}

func onnxBuild(ctx context.Context) (Embedder, error) {
	tok, err := newBertTokenizer(onnxReady.vocabPath)
	if err != nil {
		return nil, fmt.Errorf("intent: loading onnx tokenizer vocab: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSession(
		onnxReady.modelPath,
		[]string{onnxInputIDsName, onnxAttentionMaskName, onnxTokenTypeIDsName},
		[]string{onnxOutputHiddenName},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("intent: creating onnx session: %w", err)
	}

	return &ONNXEmbedder{
		session: session,
		tok:     tok,
		dims:    onnxReady.dims,
		model:   onnxReady.modelName,
	}, nil
}

// ONNXEmbedder runs the pinned model in-process via ONNX Runtime. No
// network calls happen at embed time — only at first-use asset download,
// handled by onnxAvailable before this type is ever constructed.
type ONNXEmbedder struct {
	// onnxruntime sessions are not documented as safe for concurrent Run
	// calls from multiple goroutines; serialize them.
	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
	tok     *bertTokenizer
	dims    int
	model   string
}

func (e *ONNXEmbedder) ModelID() string { return "onnx:" + e.model }

func (e *ONNXEmbedder) Dimensions() int { return e.dims }

func (e *ONNXEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	inputIDs, attentionMask, tokenTypeIDs, seqLen := e.tok.encodeBatch(texts, onnxMaxSeqLen)
	batch := len(texts)
	shape := ort.NewShape(int64(batch), int64(seqLen))

	idsTensor, err := ort.NewTensor(shape, flattenRows(inputIDs))
	if err != nil {
		return nil, fmt.Errorf("intent: building input_ids tensor: %w", err)
	}
	defer idsTensor.Destroy()

	maskTensor, err := ort.NewTensor(shape, flattenRows(attentionMask))
	if err != nil {
		return nil, fmt.Errorf("intent: building attention_mask tensor: %w", err)
	}
	defer maskTensor.Destroy()

	typeTensor, err := ort.NewTensor(shape, flattenRows(tokenTypeIDs))
	if err != nil {
		return nil, fmt.Errorf("intent: building token_type_ids tensor: %w", err)
	}
	defer typeTensor.Destroy()

	outputs := []ort.Value{nil}

	e.mu.Lock()
	runErr := e.session.Run([]ort.Value{idsTensor, maskTensor, typeTensor}, outputs)
	e.mu.Unlock()
	if runErr != nil {
		return nil, fmt.Errorf("intent: running onnx session: %w", runErr)
	}
	defer outputs[0].Destroy()

	hidden, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("intent: unexpected onnx output tensor type %T", outputs[0])
	}

	return meanPoolNormalize(hidden.GetData(), attentionMask, batch, seqLen, e.dims), nil
}

func flattenRows(rows [][]int64) []int64 {
	if len(rows) == 0 {
		return nil
	}
	out := make([]int64, 0, len(rows)*len(rows[0]))
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}
