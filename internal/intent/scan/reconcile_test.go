package scan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/intent"
	"github.com/swordsmanluke/cloche/internal/intent/scan"
)

func newTestStore(t *testing.T) *intent.Store {
	t.Helper()
	return intent.NewStore(t.TempDir())
}

func candidate(statement string) scan.CandidateFields {
	return scan.CandidateFields{
		Statement:  statement,
		Rationale:  "because reasons",
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Provenance: intent.Provenance{Kind: intent.ProvenanceDoc, Ref: "CLAUDE.md"},
	}
}

func TestApply_Create(t *testing.T) {
	store := newTestStore(t)
	actions := []scan.ReconcileAction{
		{Action: scan.ActionCreate, CandidateFields: candidate("Never bump major without asking.")},
	}

	report, err := scan.Apply(store, actions)
	require.NoError(t, err)
	require.Len(t, report.Created, 1)

	reqs, err := store.ListRequirements()
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	assert.Equal(t, intent.StatusActive, reqs[0].Status)
	assert.Contains(t, reqs[0].Body, "Never bump major without asking.")
	assert.Contains(t, reqs[0].Body, "because reasons")
}

func TestApply_Supersede(t *testing.T) {
	store := newTestStore(t)
	old, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "old statement",
	})
	require.NoError(t, err)

	actions := []scan.ReconcileAction{
		{Action: scan.ActionSupersede, ExistingID: old.ID, CandidateFields: candidate("new statement")},
	}
	report, err := scan.Apply(store, actions)
	require.NoError(t, err)
	require.Len(t, report.Superseded, 1)
	require.Len(t, report.Created, 0, "supersede reports via Superseded, not Created")

	reloadedOld, err := store.GetRequirement(old.ID)
	require.NoError(t, err)
	assert.Equal(t, intent.StatusSuperseded, reloadedOld.Status)
	assert.Equal(t, "old statement", reloadedOld.Body, "the superseded requirement's body must be untouched")
	assert.NotEmpty(t, reloadedOld.SupersededBy)

	newReq, err := store.GetRequirement(reloadedOld.SupersededBy)
	require.NoError(t, err)
	assert.Equal(t, intent.StatusActive, newReq.Status)
	assert.Contains(t, newReq.Body, "new statement")
}

func TestApply_MergeAndDropAreNoOps(t *testing.T) {
	store := newTestStore(t)
	existing, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "existing",
	})
	require.NoError(t, err)

	actions := []scan.ReconcileAction{
		{Action: scan.ActionMerge, ExistingID: existing.ID},
		{Action: scan.ActionDrop},
	}
	report, err := scan.Apply(store, actions)
	require.NoError(t, err)
	assert.Equal(t, []string{existing.ID}, report.Merged)
	assert.Equal(t, 1, report.Dropped)

	reloaded, err := store.GetRequirement(existing.ID)
	require.NoError(t, err)
	assert.Equal(t, "existing", reloaded.Body)
	assert.Equal(t, intent.StatusActive, reloaded.Status)
}

func TestApply_NeverReenablesDisabled(t *testing.T) {
	store := newTestStore(t)
	disabled, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusDisabled,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "disabled requirement",
	})
	require.NoError(t, err)

	for _, act := range []scan.Action{scan.ActionSupersede, scan.ActionMerge} {
		actions := []scan.ReconcileAction{
			{Action: act, ExistingID: disabled.ID, CandidateFields: candidate("new statement")},
		}
		_, err := scan.Apply(store, actions)
		require.Error(t, err, "action %s against a disabled requirement must be rejected", act)
		var verr *scan.ValidationError
		require.ErrorAs(t, err, &verr)
		require.Len(t, verr.Violations, 1)
		assert.Equal(t, "target_not_active", verr.Violations[0].Rule)
	}

	reloaded, err := store.GetRequirement(disabled.ID)
	require.NoError(t, err)
	assert.Equal(t, intent.StatusDisabled, reloaded.Status, "still disabled — nothing was applied")
}

func TestApply_NeverRewritesUserEditedInPlace(t *testing.T) {
	store := newTestStore(t)
	edited, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"versioning"}},
		Confidence: intent.ConfidenceHigh,
		UserEdited: true,
		Body:       "the user's own wording",
	})
	require.NoError(t, err)

	// The only permitted operation on a user_edited requirement is supersede.
	actions := []scan.ReconcileAction{
		{Action: scan.ActionSupersede, ExistingID: edited.ID, CandidateFields: candidate("machine-proposed replacement")},
	}
	_, err = scan.Apply(store, actions)
	require.NoError(t, err)

	reloaded, err := store.GetRequirement(edited.ID)
	require.NoError(t, err)
	assert.Equal(t, "the user's own wording", reloaded.Body, "statement text must survive untouched")
	assert.Equal(t, []string{"versioning"}, reloaded.Scope.Domains, "scope must survive untouched")
	assert.True(t, reloaded.UserEdited)
	assert.Equal(t, intent.StatusSuperseded, reloaded.Status)
}

func TestApply_NeverDeletes(t *testing.T) {
	store := newTestStore(t)
	existing, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "existing",
	})
	require.NoError(t, err)

	// There is no "delete" action in the schema at all; an attempt to smuggle
	// one in is rejected as an invalid action rather than silently ignored.
	actions := []scan.ReconcileAction{
		{Action: scan.Action("delete"), ExistingID: existing.ID},
	}
	_, err = scan.Apply(store, actions)
	require.Error(t, err)
	var verr *scan.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "invalid_action", verr.Violations[0].Rule)

	reqs, err := store.ListRequirements()
	require.NoError(t, err)
	require.Len(t, reqs, 1, "the requirement file must still exist")
}

func TestApply_ValidationIsAllOrNothing(t *testing.T) {
	store := newTestStore(t)
	actions := []scan.ReconcileAction{
		{Action: scan.ActionCreate, CandidateFields: candidate("a fine new requirement")},
		{Action: scan.ActionSupersede, ExistingID: "req-missing", CandidateFields: candidate("replacement")},
	}
	_, err := scan.Apply(store, actions)
	require.Error(t, err)

	reqs, err := store.ListRequirements()
	require.NoError(t, err)
	assert.Empty(t, reqs, "no action should be applied when any action in the batch is invalid")
}

func TestParseAndMarshalCandidates_RoundTrip(t *testing.T) {
	in := []scan.Candidate{{CandidateFields: candidate("round trip me")}}
	data, err := scan.MarshalCandidates(in)
	require.NoError(t, err)

	out, err := scan.ParseCandidates(data)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "round trip me", out[0].Statement)
}

func TestParseAndMarshalReconcileActions_RoundTrip(t *testing.T) {
	in := []scan.ReconcileAction{{Action: scan.ActionCreate, CandidateFields: candidate("round trip me")}}
	data, err := scan.MarshalReconcileActions(in)
	require.NoError(t, err)

	out, err := scan.ParseReconcileActions(data)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, scan.ActionCreate, out[0].Action)
	assert.Equal(t, "round trip me", out[0].Statement)
}
