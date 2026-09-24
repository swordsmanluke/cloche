package scan

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/intent"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestParseCandidatesCSV_HappyPath(t *testing.T) {
	data := CandidatesCSVHeader + "\n" +
		`"Never bump the major version unless told to.","Batched manually.",domain,versioning,"is this a major bump;breaking change version bump",high,doc,CLAUDE.md#versioning` + "\n" +
		`"Errors are one line: line N: <message>.",,project,,"traceback in output;why two lines of error",medium,commit,abc123` + "\n"
	cands, problems := ParseCandidatesCSV([]byte(data), map[string]bool{"versioning": true}, now)
	require.Empty(t, problems)
	require.Len(t, cands, 2)
	assert.Equal(t, intent.ScopeLevelDomain, cands[0].Scope.Level)
	assert.Equal(t, []string{"versioning"}, cands[0].Scope.Domains)
	assert.Equal(t, []string{"is this a major bump", "breaking change version bump"}, cands[0].Hints)
	assert.Equal(t, intent.ProvenanceDoc, cands[0].Provenance.Kind)
	assert.Equal(t, now, cands[0].Provenance.ExtractedAt)
	assert.Equal(t, "intent-scan", cands[0].Provenance.ExtractedBy)
	assert.Equal(t, intent.ScopeLevelProject, cands[1].Scope.Level)
	assert.Nil(t, cands[1].Scope.Domains)
	assert.Contains(t, cands[1].Statement, "line N: <message>")
}

func TestParseCandidatesCSV_EmptyIsValid(t *testing.T) {
	cands, problems := ParseCandidatesCSV([]byte(CandidatesCSVHeader+"\n"), nil, now)
	assert.Empty(t, problems)
	assert.NotNil(t, cands)
	assert.Len(t, cands, 0)
}

func TestParseCandidatesCSV_Problems(t *testing.T) {
	data := CandidatesCSVHeader + "\n" +
		`,r,domain,,"h",HIGH,doc,ref` + "\n" + // empty statement, no domains
		`s,r,galaxy,,h,high,doc,ref` + "\n" + // bad scope
		`s,r,domain,nope,,sure,email,` + "\n" // unknown domain, no hints, bad confidence, bad kind, no ref
	cands, problems := ParseCandidatesCSV([]byte(data), map[string]bool{"versioning": true}, now)
	assert.Nil(t, cands)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"empty statement", "no domains", "scope_level must be", "unknown domain \"nope\"",
		"no hints", "confidence must be", "provenance_kind must be", "empty provenance_ref"} {
		assert.Contains(t, joined, want)
	}
	_, problems = ParseCandidatesCSV([]byte("statement,foo\nx,y\n"), nil, now)
	assert.Contains(t, problems[0], "header must be exactly")
}

func TestParseReconcileCSV_FillsBlanksFromCandidateAndChecksShape(t *testing.T) {
	cands := []Candidate{
		{CandidateFields: CandidateFields{Statement: "S1", Rationale: "R1", Scope: intent.Scope{Level: intent.ScopeLevelProject},
			Hints: []string{"h1", "h2"}, Confidence: intent.ConfidenceHigh, Provenance: intent.Provenance{Kind: intent.ProvenanceDoc, Ref: "D"}}},
		{CandidateFields: CandidateFields{Statement: "S2", Scope: intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"cli"}},
			Hints: []string{"h"}, Confidence: intent.ConfidenceLow, Provenance: intent.Provenance{Kind: intent.ProvenanceCommit, Ref: "abc"}}},
		{CandidateFields: CandidateFields{Statement: "S3", Scope: intent.Scope{Level: intent.ScopeLevelProject},
			Hints: []string{"h"}, Confidence: intent.ConfidenceLow, Provenance: intent.Provenance{Kind: intent.ProvenanceCommit, Ref: "abc"}}},
	}
	data := ReconcileCSVHeader + "\n" +
		`1,create,,,,,,,,,,` + "\n" + // everything from the candidate
		`2,supersede,req-b91c,"commit abc reverses this","S2 (clarified)",,,,,,,` + "\n" +
		`3,drop,,task-specific,,,,,,,,` + "\n"
	actions, problems := ParseReconcileCSV([]byte(data), cands, map[string]bool{"cli": true}, now)
	require.Empty(t, problems)
	require.Len(t, actions, 3)
	assert.Equal(t, ActionCreate, actions[0].Action)
	assert.Equal(t, "S1", actions[0].Statement)
	assert.Equal(t, []string{"h1", "h2"}, actions[0].Hints)
	assert.Equal(t, intent.ProvenanceDoc, actions[0].Provenance.Kind)
	assert.Equal(t, ActionSupersede, actions[1].Action)
	assert.Equal(t, "req-b91c", actions[1].ExistingID)
	assert.Equal(t, "S2 (clarified)", actions[1].Statement)
	assert.Equal(t, []string{"cli"}, actions[1].Scope.Domains)
	assert.Equal(t, ActionDrop, actions[2].Action)
	assert.Equal(t, "task-specific", actions[2].Reason)

	bad := ReconcileCSVHeader + "\n" +
		`1,merge,,,,,,,,,,` + "\n" + // merge without id
		`1,keep,x,,,,,,,,,` + "\n" // duplicate index, bad action, bad id, out of order, and only 2 rows for 3 candidates
	_, problems = ParseReconcileCSV([]byte(bad), cands, nil, now)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"one row per candidate", "merge needs existing_id", "appears more than once",
		"action must be", "does not look like a requirement id", "candidate order"} {
		assert.Contains(t, joined, want)
	}
}
