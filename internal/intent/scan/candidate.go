package scan

import (
	"encoding/json"
	"fmt"

	"github.com/cloche-dev/cloche/internal/intent"
)

// CandidateFields are the fields the extract step writes for a proposed
// requirement, and that the reconcile step re-states in full for a "create"
// or "supersede" action (so applying a reconcile decision never has to
// cross-reference a second file).
type CandidateFields struct {
	Statement  string            `json:"statement"`
	Rationale  string            `json:"rationale,omitempty"`
	Scope      intent.Scope      `json:"scope"`
	Hints      []string          `json:"hints,omitempty"`
	Confidence intent.Confidence `json:"confidence"`
	Provenance intent.Provenance `json:"provenance"`
}

// Body renders the candidate's statement and optional rationale into the
// markdown body of a requirement file (see intent.Requirement.Body).
func (f CandidateFields) Body() string {
	if f.Rationale == "" {
		return f.Statement
	}
	return f.Statement + "\n\n**Why:** " + f.Rationale
}

// Candidate is one proposed requirement written by the extract step to
// candidates.json.
type Candidate struct {
	CandidateFields
}

// candidatesFile is the on-disk shape of candidates.json.
type candidatesFile struct {
	Candidates []Candidate `json:"candidates"`
}

// ParseCandidates decodes the extract step's candidates.json output.
func ParseCandidates(data []byte) ([]Candidate, error) {
	var f candidatesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("intent scan: parsing candidates.json: %w", err)
	}
	return f.Candidates, nil
}

// MarshalCandidates encodes candidates back to the candidates.json shape
// (used by tests to build extract-step fixtures).
func MarshalCandidates(candidates []Candidate) ([]byte, error) {
	return json.MarshalIndent(candidatesFile{Candidates: candidates}, "", "  ")
}
