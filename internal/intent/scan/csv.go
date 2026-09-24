package scan

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/intent"
)

// The extract and reconcile agents hand off CSV, never JSON: quoting is the
// only rule an agent has to get right, and every problem is reported as a
// line a repair step can hand straight back. The JSON files apply-reconcile
// consumes are written by the check scripts from these parsers.

// CandidatesCSVHeader is the exact header line of candidates.csv.
const CandidatesCSVHeader = "statement,rationale,scope_level,domains,hints,confidence,provenance_kind,provenance_ref"

// ReconcileCSVHeader is the exact header line of reconcile.csv.
const ReconcileCSVHeader = "candidate,action,existing_id,reason,statement,rationale,scope_level,domains,hints,confidence,provenance_kind,provenance_ref"

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readCSV reads a CSV with the given exact header, returning the rows (each
// padded to len(header)) and the line number of each row.
func readCSV(data []byte, header string) (rows [][]string, lines []int, problems []string) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, utf8BOM)))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	want := strings.Split(header, ",")

	got, err := r.Read()
	if err == io.EOF {
		return nil, nil, []string{"the file is empty; expected a header line: " + header}
	}
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("line 1: %v", err)}
	}
	if len(got) != len(want) {
		return nil, nil, []string{fmt.Sprintf("line 1: header must be exactly %q (got %d columns)", header, len(got))}
	}
	for i := range want {
		if strings.ToLower(strings.TrimSpace(got[i])) != want[i] {
			return nil, nil, []string{fmt.Sprintf("line 1: header must be exactly %q (column %d is %q, expected %q)", header, i+1, got[i], want[i])}
		}
	}
	line := 1
	for {
		rec, err := r.Read()
		line++
		if err == io.EOF {
			break
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if len(rec) != len(want) {
			problems = append(problems, fmt.Sprintf("line %d: expected %d columns, got %d — quote any field that contains a comma or a line break", line, len(want), len(rec)))
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		rows = append(rows, rec)
		lines = append(lines, line)
	}
	return rows, lines, problems
}

// fieldsFromRow validates and converts the shared candidate columns
// (statement..provenance_ref, given as a slice in that order). domainNames
// is the set of known domains (nil/empty = not enforced).
func fieldsFromRow(line int, label string, cols []string, domainNames map[string]bool, now time.Time) (CandidateFields, []string) {
	var problems []string
	f := CandidateFields{
		Statement:  cols[0],
		Rationale:  cols[1],
		Scope:      intent.Scope{Level: intent.ScopeLevel(strings.ToLower(cols[2])), Domains: splitList(cols[3])},
		Hints:      splitList(cols[4]),
		Confidence: intent.Confidence(strings.ToLower(cols[5])),
		Provenance: intent.Provenance{
			Kind:        intent.ProvenanceKind(strings.ToLower(cols[6])),
			Ref:         cols[7],
			ExtractedAt: now,
			ExtractedBy: "intent-scan",
		},
	}
	if f.Statement == "" {
		problems = append(problems, fmt.Sprintf("line %d: %s has an empty statement", line, label))
	}
	switch f.Scope.Level {
	case intent.ScopeLevelProject:
		f.Scope.Domains = nil
	case intent.ScopeLevelDomain:
		if len(f.Scope.Domains) == 0 {
			problems = append(problems, fmt.Sprintf("line %d: %s has scope_level=domain but no domains (separate several with ';')", line, label))
		}
		for _, d := range f.Scope.Domains {
			if len(domainNames) > 0 && !domainNames[d] {
				problems = append(problems, fmt.Sprintf("line %d: %s names unknown domain %q (see domains.yaml)", line, label, d))
			}
		}
	default:
		problems = append(problems, fmt.Sprintf("line %d: %s scope_level must be project or domain (got %q)", line, label, cols[2]))
	}
	switch f.Confidence {
	case intent.ConfidenceHigh, intent.ConfidenceMedium, intent.ConfidenceLow:
	default:
		problems = append(problems, fmt.Sprintf("line %d: %s confidence must be high, medium or low (got %q)", line, label, cols[5]))
	}
	switch f.Provenance.Kind {
	case intent.ProvenanceDoc, intent.ProvenanceTranscript, intent.ProvenancePrompt, intent.ProvenanceCommit:
	default:
		problems = append(problems, fmt.Sprintf("line %d: %s provenance_kind must be doc, transcript, prompt or commit (got %q)", line, label, cols[6]))
	}
	if f.Provenance.Ref == "" {
		problems = append(problems, fmt.Sprintf("line %d: %s has an empty provenance_ref", line, label))
	}
	if len(f.Hints) == 0 {
		problems = append(problems, fmt.Sprintf("line %d: %s has no hints (2–5, separated by ';')", line, label))
	} else if len(f.Hints) > 8 {
		problems = append(problems, fmt.Sprintf("line %d: %s has %d hints; keep it to 2–5", line, label, len(f.Hints)))
	}
	return f, problems
}

// ParseCandidatesCSV decodes candidates.csv. A header-only file is a valid
// empty result. A non-empty problems slice means the candidates must not be
// used.
func ParseCandidatesCSV(data []byte, domainNames map[string]bool, now time.Time) ([]Candidate, []string) {
	rows, lines, problems := readCSV(data, CandidatesCSVHeader)
	if len(problems) > 0 && rows == nil {
		return nil, problems
	}
	var out []Candidate
	for i, row := range rows {
		f, p := fieldsFromRow(lines[i], fmt.Sprintf("candidate %d", i+1), row, domainNames, now)
		problems = append(problems, p...)
		out = append(out, Candidate{CandidateFields: f})
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if out == nil {
		out = []Candidate{}
	}
	return out, nil
}

// ParseReconcileCSV decodes reconcile.csv against the candidates it answers.
// Exactly one row per candidate, in candidate order (the `candidate` column
// is the 1-based index). For create/supersede, any blank candidate column is
// filled from the candidate itself, so the agent only has to restate what it
// changed. Hard-rule validation against existing requirements is the
// caller's job (Validate); this only checks shape.
func ParseReconcileCSV(data []byte, candidates []Candidate, domainNames map[string]bool, now time.Time) ([]ReconcileAction, []string) {
	rows, lines, problems := readCSV(data, ReconcileCSVHeader)
	if len(problems) > 0 && rows == nil {
		return nil, problems
	}
	if len(rows) != len(candidates) {
		problems = append(problems, fmt.Sprintf("expected exactly one row per candidate (%d candidates, got %d rows)", len(candidates), len(rows)))
	}
	seen := map[int]bool{}
	var out []ReconcileAction
	for i, row := range rows {
		line := lines[i]
		idx, err := strconv.Atoi(row[0])
		if err != nil || idx < 1 || idx > len(candidates) {
			problems = append(problems, fmt.Sprintf("line %d: candidate must be a 1-based index into the %d candidates (got %q)", line, len(candidates), row[0]))
			continue
		}
		if seen[idx] {
			problems = append(problems, fmt.Sprintf("line %d: candidate %d appears more than once", line, idx))
		}
		seen[idx] = true
		if idx != i+1 {
			problems = append(problems, fmt.Sprintf("line %d: rows must be in candidate order (expected candidate %d here, got %d)", line, i+1, idx))
		}
		a := ReconcileAction{
			Action:     Action(strings.ToLower(row[1])),
			ExistingID: row[2],
			Reason:     row[3],
		}
		cand := candidates[idx-1]
		switch a.Action {
		case ActionCreate, ActionSupersede:
			cols := append([]string(nil), row[4:]...)
			// Blank columns mean "as in the candidate".
			if cols[0] == "" {
				cols[0] = cand.Statement
			}
			if cols[1] == "" {
				cols[1] = cand.Rationale
			}
			if cols[2] == "" {
				cols[2] = string(cand.Scope.Level)
			}
			if cols[3] == "" {
				cols[3] = strings.Join(cand.Scope.Domains, ";")
			}
			if cols[4] == "" {
				cols[4] = strings.Join(cand.Hints, ";")
			}
			if cols[5] == "" {
				cols[5] = string(cand.Confidence)
			}
			if cols[6] == "" {
				cols[6] = string(cand.Provenance.Kind)
			}
			if cols[7] == "" {
				cols[7] = cand.Provenance.Ref
			}
			f, p := fieldsFromRow(line, fmt.Sprintf("action for candidate %d", idx), cols, domainNames, now)
			problems = append(problems, p...)
			a.CandidateFields = f
			if a.Action == ActionSupersede && a.ExistingID == "" {
				problems = append(problems, fmt.Sprintf("line %d: supersede needs existing_id (the requirement being superseded)", line))
			}
		case ActionMerge:
			if a.ExistingID == "" {
				problems = append(problems, fmt.Sprintf("line %d: merge needs existing_id (the requirement it duplicates)", line))
			}
		case ActionDrop:
		default:
			problems = append(problems, fmt.Sprintf("line %d: action must be create, merge, supersede or drop (got %q)", line, row[1]))
		}
		if a.ExistingID != "" && !strings.HasPrefix(a.ExistingID, "req-") {
			problems = append(problems, fmt.Sprintf("line %d: existing_id %q does not look like a requirement id (req-xxxx)", line, a.ExistingID))
		}
		out = append(out, a)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return out, nil
}
