// Package intent implements the file-backed store for project "requirements" —
// durable statements of intent (constraints and decisions) that are extracted
// from project sources and injected into agent-step prompts. See
// docs/plans/2026-09-13-intent-continuity-design.md for the full design.
package intent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Status is the lifecycle state of a Requirement.
type Status string

const (
	StatusActive     Status = "active"
	StatusDisabled   Status = "disabled"
	StatusSuperseded Status = "superseded"
)

// ScopeLevel controls whether a Requirement is always injected or only for
// matching domains.
type ScopeLevel string

const (
	ScopeLevelProject ScopeLevel = "project"
	ScopeLevelDomain  ScopeLevel = "domain"
)

// Confidence is the extractor's judgment of how durable/certain a Requirement is.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// ProvenanceKind identifies the kind of source a Requirement was extracted from.
type ProvenanceKind string

const (
	ProvenanceDoc        ProvenanceKind = "doc"
	ProvenanceTranscript ProvenanceKind = "transcript"
	ProvenancePrompt     ProvenanceKind = "prompt"
	ProvenanceCommit     ProvenanceKind = "commit"
	ProvenanceUser       ProvenanceKind = "user"
)

// Scope describes what a Requirement applies to: always ("project" level) or
// narrowed to one or more domains, optionally further narrowed by path globs
// or languages. Repos narrows it further still, to the named repositories
// of a multi-repo project.
type Scope struct {
	Level     ScopeLevel `yaml:"level" json:"level"`
	Domains   []string   `yaml:"domains,omitempty" json:"domains,omitempty"`
	Paths     []string   `yaml:"paths,omitempty" json:"paths,omitempty"`
	Languages []string   `yaml:"languages,omitempty" json:"languages,omitempty"`
	// Repos lists the [[repositories]] names this requirement applies to.
	// Empty means global: it applies to every repo and to the project root
	// itself. A requirement mined from one repo is tagged with that repo
	// and is never shown to steps working on another — most requirements
	// only make sense inside the project they came from, and applying them
	// elsewhere creates bugs.
	Repos []string `yaml:"repos,omitempty" json:"repos,omitempty"`
}

// VisibleTo reports whether a requirement with this scope applies when
// working on repo (a [[repositories]] name, or "" for the project root /
// a step that declares no repos). A global scope is visible everywhere; a
// repo-scoped one only to the repos it names.
func (s Scope) VisibleTo(repo string) bool {
	if len(s.Repos) == 0 {
		return true
	}
	for _, r := range s.Repos {
		if r == repo {
			return true
		}
	}
	return false
}

// VisibleToAny reports whether this scope applies to at least one of repos.
// With no repos given it reduces to VisibleTo("") — global requirements
// only, which is what a step with no declared repos receives.
func (s Scope) VisibleToAny(repos []string) bool {
	if len(repos) == 0 {
		return s.VisibleTo("")
	}
	for _, r := range repos {
		if s.VisibleTo(r) {
			return true
		}
	}
	return false
}

// Provenance records where a Requirement came from.
type Provenance struct {
	Kind        ProvenanceKind `yaml:"kind" json:"kind"`
	Ref         string         `yaml:"ref" json:"ref"`
	ExtractedAt time.Time      `yaml:"extracted_at" json:"extracted_at"`
	ExtractedBy string         `yaml:"extracted_by" json:"extracted_by"`
}

// Requirement is one durable statement of intent: a constraint or decision,
// with provenance, scope, status, and confidence. Body holds the markdown
// statement (plus optional rationale) that follows the YAML frontmatter in
// the requirement file; it is never part of the YAML itself.
type Requirement struct {
	ID           string     `yaml:"id"`
	Status       Status     `yaml:"status"`
	SupersededBy string     `yaml:"superseded_by,omitempty"`
	Scope        Scope      `yaml:"scope"`
	Hints        []string   `yaml:"hints,omitempty"`
	Confidence   Confidence `yaml:"confidence"`
	UserEdited   bool       `yaml:"user_edited"`
	Provenance   Provenance `yaml:"provenance"`
	Created      time.Time  `yaml:"created"`
	Updated      time.Time  `yaml:"updated"`
	Body         string     `yaml:"-"`
}

// Domain is one of the project's major architectural systems, as discovered
// by an intent-scan and/or hand-edited by the user.
type Domain struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Paths       []string `yaml:"paths"`
	UserEdited  bool     `yaml:"user_edited,omitempty"`
}

// DomainMap is the full contents of .cloche/intent/domains.yaml.
type DomainMap struct {
	Version int      `yaml:"version"`
	Domains []Domain `yaml:"domains"`
}

const frontmatterDelim = "---"

// ParseRequirement parses a requirement file (YAML frontmatter delimited by
// "---" lines, followed by a markdown body). It returns an error — never a
// panic — for malformed input: missing delimiters or invalid YAML.
func ParseRequirement(data []byte) (*Requirement, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelim {
		return nil, fmt.Errorf("intent: requirement file must start with a %q frontmatter delimiter", frontmatterDelim)
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterDelim {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("intent: requirement file is missing the closing %q frontmatter delimiter", frontmatterDelim)
	}

	fm := strings.Join(lines[1:end], "\n")
	body := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))

	var req Requirement
	if err := yaml.Unmarshal([]byte(fm), &req); err != nil {
		return nil, fmt.Errorf("intent: parsing requirement frontmatter: %w", err)
	}
	req.Body = body
	return &req, nil
}

// MarshalRequirement serializes a Requirement back into the markdown +
// frontmatter file format parsed by ParseRequirement.
func MarshalRequirement(req *Requirement) ([]byte, error) {
	fm, err := yaml.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("intent: marshaling requirement frontmatter: %w", err)
	}

	var buf strings.Builder
	buf.WriteString(frontmatterDelim)
	buf.WriteString("\n")
	buf.Write(fm)
	buf.WriteString(frontmatterDelim)
	buf.WriteString("\n\n")
	buf.WriteString(req.Body)
	buf.WriteString("\n")
	return []byte(buf.String()), nil
}

// GenerateRequirementID returns a random requirement ID of the form
// "req-xxxx" where xxxx is 4 lowercase hex characters. Panics if the system
// CSPRNG fails (mirrors domain.GenerateAttemptID's convention).
func GenerateRequirementID() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return "req-" + hex.EncodeToString(b)
}
