package embed

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeanPoolNormalize_AveragesOnlyAttendedPositions(t *testing.T) {
	// batch=1, seqLen=3, dims=2. Position 2 is padding (mask=0) and must
	// not contribute to the mean.
	hidden := []float32{
		1, 0, // position 0
		3, 0, // position 1
		999, 999, // position 2 (padding — must be ignored)
	}
	mask := [][]int64{{1, 1, 0}}

	out := meanPoolNormalize(hidden, mask, 1, 3, 2)
	require.Len(t, out, 1)

	// Mean of (1,0) and (3,0) is (2,0), which normalizes to (1,0).
	assert.InDelta(t, 1.0, out[0][0], 1e-6)
	assert.InDelta(t, 0.0, out[0][1], 1e-6)
}

func TestMeanPoolNormalize_OutputIsUnitLength(t *testing.T) {
	hidden := []float32{
		1, 2, 3,
		4, 5, 6,
	}
	mask := [][]int64{{1, 1}}

	out := meanPoolNormalize(hidden, mask, 1, 2, 3)
	require.Len(t, out, 1)

	var sumSq float64
	for _, v := range out[0] {
		sumSq += float64(v) * float64(v)
	}
	assert.InDelta(t, 1.0, math.Sqrt(sumSq), 1e-6)
}

func TestMeanPoolNormalize_AllPaddingYieldsZeroVectorWithoutPanic(t *testing.T) {
	hidden := []float32{1, 2}
	mask := [][]int64{{0}}

	out := meanPoolNormalize(hidden, mask, 1, 1, 2)
	require.Len(t, out, 1)
	assert.Equal(t, []float32{0, 0}, out[0])
}

func TestMeanPoolNormalize_MultipleBatchRowsAreIndependent(t *testing.T) {
	hidden := []float32{
		// row 0, seq 0..1
		2, 0,
		2, 0,
		// row 1, seq 0..1
		0, 5,
		0, 5,
	}
	mask := [][]int64{{1, 1}, {1, 1}}

	out := meanPoolNormalize(hidden, mask, 2, 2, 2)
	require.Len(t, out, 2)
	assert.InDelta(t, 1.0, out[0][0], 1e-6)
	assert.InDelta(t, 0.0, out[0][1], 1e-6)
	assert.InDelta(t, 0.0, out[1][0], 1e-6)
	assert.InDelta(t, 1.0, out[1][1], 1e-6)
}
