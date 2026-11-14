package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecoveryPrompt_ListsDeclaredResults(t *testing.T) {
	got := recoveryPrompt("abc123", []string{"success", "fail"})
	assert.Contains(t, got, "success")
	assert.Contains(t, got, "fail")
	assert.Contains(t, got, "CLOCHE_RESULT:abc123:")
	assert.Contains(t, got, "No other result name is valid")
}
