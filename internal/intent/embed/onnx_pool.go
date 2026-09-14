package embed

// meanPoolNormalize turns a [batch, seqLen, dims] token-embedding tensor
// (row-major flattened, as returned by the MiniLM ONNX export's
// last_hidden_state output) into one unit-normalized sentence vector per
// batch row, using mean pooling over the non-padding positions per
// attentionMask (the sentence-transformers pooling strategy for this
// model family). Kept free of any onnxruntime dependency so it is
// unit-testable without the onnx build tag.
func meanPoolNormalize(hidden []float32, attentionMask [][]int64, batch, seqLen, dims int) [][]float32 {
	out := make([][]float32, batch)
	for b := 0; b < batch; b++ {
		sum := make([]float32, dims)
		var count float32
		for s := 0; s < seqLen; s++ {
			if attentionMask[b][s] == 0 {
				continue
			}
			base := (b*seqLen + s) * dims
			for d := 0; d < dims; d++ {
				sum[d] += hidden[base+d]
			}
			count++
		}
		if count > 0 {
			for d := 0; d < dims; d++ {
				sum[d] /= count
			}
		}
		normalize(sum)
		out[b] = sum
	}
	return out
}
