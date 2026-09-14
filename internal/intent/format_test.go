package intent_test

import (
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/stretchr/testify/assert"
)

func TestFormatBlock_EmptySelectionRendersNothing(t *testing.T) {
	assert.Equal(t, "", intent.FormatBlock(intent.Result{}))
}

func TestFormatBlock_ProjectLevelLineHasNoDomainParenthetical(t *testing.T) {
	res := intent.Result{Selected: []intent.Selected{
		{Requirement: &intent.Requirement{
			ID:    "req-a3f8",
			Scope: intent.Scope{Level: intent.ScopeLevelProject},
			Body:  "Never bump the major version unless explicitly told to.",
		}, Score: -1},
	}}

	block := intent.FormatBlock(res)
	assert.Contains(t, block, "## Standing project requirements")
	assert.Contains(t, block, "- [req-a3f8] Never bump the major version unless explicitly told to.")
}

func TestFormatBlock_DomainScopedLineIncludesDomains(t *testing.T) {
	res := intent.Result{Selected: []intent.Selected{
		{Requirement: &intent.Requirement{
			ID:    "req-b91c",
			Scope: intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"container-runtime"}},
			Body:  "The local adapter is for tests only; real runs go through Docker.",
		}, Score: 0.8},
	}}

	block := intent.FormatBlock(res)
	assert.Contains(t, block, "- [req-b91c] (container-runtime) The local adapter is for tests only; real runs go through Docker.")
}

func TestFormatBlock_CollapsesMultilineBody(t *testing.T) {
	res := intent.Result{Selected: []intent.Selected{
		{Requirement: &intent.Requirement{
			ID:    "req-c001",
			Scope: intent.Scope{Level: intent.ScopeLevelProject},
			Body:  "Statement line one.\n\n**Why:** rationale line two.",
		}, Score: -1},
	}}

	block := intent.FormatBlock(res)
	for _, line := range strings.Split(strings.TrimSpace(block), "\n") {
		assert.NotContains(t, line, "\n")
	}
	assert.Contains(t, block, "- [req-c001] Statement line one. **Why:** rationale line two.")
}

func TestFormatBlock_OmittedAddsMarker(t *testing.T) {
	res := intent.Result{
		Selected: []intent.Selected{
			{Requirement: &intent.Requirement{ID: "req-0001", Scope: intent.Scope{Level: intent.ScopeLevelProject}, Body: "kept"}, Score: -1},
		},
		Omitted: 3,
	}

	block := intent.FormatBlock(res)
	assert.Contains(t, block, "(3 more requirements omitted; run cloche intent list)")
}

func TestFormatBlock_NoOmittedHasNoMarker(t *testing.T) {
	res := intent.Result{Selected: []intent.Selected{
		{Requirement: &intent.Requirement{ID: "req-0001", Scope: intent.Scope{Level: intent.ScopeLevelProject}, Body: "kept"}, Score: -1},
	}}

	block := intent.FormatBlock(res)
	assert.NotContains(t, block, "omitted")
}
