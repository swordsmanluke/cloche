package intent_test

import (
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMarshalRequirement_RoundTrip(t *testing.T) {
	extractedAt := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	created := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 9, 14, 8, 30, 0, 0, time.UTC)

	req := &intent.Requirement{
		ID:           "req-a3f8",
		Status:       intent.StatusActive,
		SupersededBy: "",
		Scope: intent.Scope{
			Level:     intent.ScopeLevelDomain,
			Domains:   []string{"versioning"},
			Paths:     []string{"internal/version/**"},
			Languages: []string{"go"},
		},
		Hints:      []string{"when to bump minor vs build number", "is this change a breaking change"},
		Confidence: intent.ConfidenceHigh,
		UserEdited: false,
		Provenance: intent.Provenance{
			Kind:        intent.ProvenanceDoc,
			Ref:         "CLAUDE.md#versioning",
			ExtractedAt: extractedAt,
			ExtractedBy: "intent-scan/jifo-intent-scan",
		},
		Created: created,
		Updated: updated,
		Body: "Never bump the major version unless explicitly told to.\n\n" +
			"**Why:** Major releases are batched manually at the maintainer's direction.",
	}

	data, err := intent.MarshalRequirement(req)
	require.NoError(t, err)

	got, err := intent.ParseRequirement(data)
	require.NoError(t, err)

	assert.Equal(t, req, got)
}

func TestParseMarshalRequirement_RoundTrip_UserEditedAndSuperseded(t *testing.T) {
	req := &intent.Requirement{
		ID:           "req-b91c",
		Status:       intent.StatusSuperseded,
		SupersededBy: "req-c001",
		Scope: intent.Scope{
			Level: intent.ScopeLevelProject,
		},
		Confidence: intent.ConfidenceMedium,
		UserEdited: true,
		Provenance: intent.Provenance{
			Kind: intent.ProvenanceUser,
			Ref:  "cloche intent add",
		},
		Body: "The local adapter is for tests only; real runs go through Docker.",
	}

	data, err := intent.MarshalRequirement(req)
	require.NoError(t, err)

	got, err := intent.ParseRequirement(data)
	require.NoError(t, err)

	assert.Equal(t, req, got)
	assert.True(t, got.UserEdited)
	assert.Equal(t, intent.StatusSuperseded, got.Status)
	assert.Equal(t, "req-c001", got.SupersededBy)
}

func TestParseRequirement_MalformedFrontmatter(t *testing.T) {
	cases := map[string]string{
		"missing opening delimiter": "id: req-a3f8\nstatus: active\n---\n\nBody text.\n",
		"missing closing delimiter": "---\nid: req-a3f8\nstatus: active\n\nBody text.\n",
		"invalid yaml": "---\nid: req-a3f8\nstatus: [this is not valid: yaml\n---\n\nBody text.\n",
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			req, err := intent.ParseRequirement([]byte(data))
			require.Error(t, err)
			assert.Nil(t, req)
		})
	}
}

func TestParseRequirement_EmptyInput(t *testing.T) {
	req, err := intent.ParseRequirement([]byte(""))
	require.Error(t, err)
	assert.Nil(t, req)
}

func TestGenerateRequirementID_Format(t *testing.T) {
	for i := 0; i < 20; i++ {
		id := intent.GenerateRequirementID()
		assert.Regexp(t, `^req-[0-9a-f]{4}$`, id)
	}
}
