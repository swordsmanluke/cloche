package domain_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputPath_Evaluate_BareOutput(t *testing.T) {
	p := domain.OutputPath{}
	val, err := p.Evaluate([]byte("hello world"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", val)
}

func TestOutputPath_Evaluate_BareOutputPreservesWhitespace(t *testing.T) {
	p := domain.OutputPath{}
	val, err := p.Evaluate([]byte("  line1\nline2  "))
	require.NoError(t, err)
	assert.Equal(t, "  line1\nline2  ", val)
}

func TestOutputPath_Evaluate_SingleField(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "name"},
		},
	}
	val, err := p.Evaluate([]byte(`{"name":"alice","age":30}`))
	require.NoError(t, err)
	assert.Equal(t, "alice", val)
}

func TestOutputPath_Evaluate_SingleIndex(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentIndex, Index: 1},
		},
	}
	val, err := p.Evaluate([]byte(`["a","b","c"]`))
	require.NoError(t, err)
	assert.Equal(t, "b", val)
}

func TestOutputPath_Evaluate_NestedFields(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "a"},
			{Kind: domain.SegmentField, Field: "b"},
			{Kind: domain.SegmentField, Field: "c"},
		},
	}
	val, err := p.Evaluate([]byte(`{"a":{"b":{"c":"deep"}}}`))
	require.NoError(t, err)
	assert.Equal(t, "deep", val)
}

func TestOutputPath_Evaluate_IndexThenField(t *testing.T) {
	// a[0].b
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "a"},
			{Kind: domain.SegmentIndex, Index: 0},
			{Kind: domain.SegmentField, Field: "b"},
		},
	}
	val, err := p.Evaluate([]byte(`{"a":[{"b":"found"}]}`))
	require.NoError(t, err)
	assert.Equal(t, "found", val)
}

func TestOutputPath_Evaluate_FieldThenIndex(t *testing.T) {
	// a.b[0]
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "a"},
			{Kind: domain.SegmentField, Field: "b"},
			{Kind: domain.SegmentIndex, Index: 0},
		},
	}
	val, err := p.Evaluate([]byte(`{"a":{"b":["first","second"]}}`))
	require.NoError(t, err)
	assert.Equal(t, "first", val)
}

func TestOutputPath_Evaluate_ComplexMixed(t *testing.T) {
	// [0].items[1].name
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentIndex, Index: 0},
			{Kind: domain.SegmentField, Field: "items"},
			{Kind: domain.SegmentIndex, Index: 1},
			{Kind: domain.SegmentField, Field: "name"},
		},
	}
	val, err := p.Evaluate([]byte(`[{"items":[{"name":"skip"},{"name":"pick"}]}]`))
	require.NoError(t, err)
	assert.Equal(t, "pick", val)
}

func TestOutputPath_Evaluate_NumberResult(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "count"},
		},
	}
	val, err := p.Evaluate([]byte(`{"count":42}`))
	require.NoError(t, err)
	assert.Equal(t, "42", val)
}

func TestOutputPath_Evaluate_BooleanResult(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "ok"},
		},
	}
	val, err := p.Evaluate([]byte(`{"ok":true}`))
	require.NoError(t, err)
	assert.Equal(t, "true", val)
}

func TestOutputPath_Evaluate_ObjectResult(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "nested"},
		},
	}
	val, err := p.Evaluate([]byte(`{"nested":{"x":1}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"x":1}`, val)
}

func TestOutputPath_Evaluate_ArrayResult(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "list"},
		},
	}
	val, err := p.Evaluate([]byte(`{"list":[1,2,3]}`))
	require.NoError(t, err)
	assert.JSONEq(t, `[1,2,3]`, val)
}

func TestOutputPath_Evaluate_NullResult(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "val"},
		},
	}
	val, err := p.Evaluate([]byte(`{"val":null}`))
	require.NoError(t, err)
	assert.Equal(t, "null", val)
}

func TestOutputPath_Evaluate_ErrorNonJSON(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "key"},
		},
	}
	_, err := p.Evaluate([]byte("not json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestOutputPath_Evaluate_ErrorMissingField(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "missing"},
		},
	}
	_, err := p.Evaluate([]byte(`{"other":"value"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestOutputPath_Evaluate_ErrorIndexOutOfRange(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentIndex, Index: 5},
		},
	}
	_, err := p.Evaluate([]byte(`[1,2,3]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestOutputPath_Evaluate_ErrorFieldOnArray(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentField, Field: "key"},
		},
	}
	_, err := p.Evaluate([]byte(`[1,2,3]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected object")
}

func TestOutputPath_Evaluate_ErrorIndexOnObject(t *testing.T) {
	p := domain.OutputPath{
		Segments: []domain.PathSegment{
			{Kind: domain.SegmentIndex, Index: 0},
		},
	}
	_, err := p.Evaluate([]byte(`{"key":"val"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected array")
}
