package scan

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloche-dev/cloche/internal/intent"
)

// Action is one reconcile decision for a candidate: create a new
// requirement, merge into (keep) an existing one, supersede an existing one
// with a new requirement, or drop the candidate entirely.
type Action string

const (
	ActionCreate    Action = "create"
	ActionMerge     Action = "merge"
	ActionSupersede Action = "supersede"
	ActionDrop      Action = "drop"
)

// ReconcileAction is one entry in the reconcile step's reconcile.json
// output: what to do with one extracted candidate.
type ReconcileAction struct {
	Action     Action `json:"action"`
	ExistingID string `json:"existing_id,omitempty"` // required for merge/supersede/drop
	Reason     string `json:"reason,omitempty"`
	CandidateFields
}

// reconcileFile is the on-disk shape of reconcile.json.
type reconcileFile struct {
	Actions []ReconcileAction `json:"actions"`
}

// ParseReconcileActions decodes the reconcile step's reconcile.json output.
func ParseReconcileActions(data []byte) ([]ReconcileAction, error) {
	var f reconcileFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("intent scan: parsing reconcile.json: %w", err)
	}
	return f.Actions, nil
}

// MarshalReconcileActions encodes actions back to the reconcile.json shape
// (used by tests to build reconcile-step fixtures).
func MarshalReconcileActions(actions []ReconcileAction) ([]byte, error) {
	return json.MarshalIndent(reconcileFile{Actions: actions}, "", "  ")
}

// Violation is a reconcile action that breaks one of the scan's hard rules
// (see the design doc's "Extraction" section): never delete a requirement,
// never re-enable a disabled one, never rewrite a user_edited one in place.
type Violation struct {
	Index  int
	Action ReconcileAction
	Rule   string
	Detail string
}

func (v Violation) Error() string {
	return fmt.Sprintf("action %d (%s): %s: %s", v.Index, v.Action.Action, v.Rule, v.Detail)
}

// Validate checks actions against existing (keyed by requirement ID) for
// hard-rule violations, without applying anything. It never flags an
// in-place rewrite of a user_edited requirement's statement or scope as a
// distinct rule, because Apply never performs one — the only mutation Apply
// makes to an existing requirement is flipping it to superseded, which is
// the one operation the design permits even for user_edited requirements.
func Validate(existing map[string]*intent.Requirement, actions []ReconcileAction) []Violation {
	var violations []Violation
	for i, a := range actions {
		switch a.Action {
		case ActionCreate:
			if a.Statement == "" {
				violations = append(violations, Violation{i, a, "missing_statement", "create requires a statement"})
			}
		case ActionDrop:
			// A drop may or may not reference an existing requirement (a
			// candidate can simply not be durable intent); when it does,
			// the reference must resolve.
			if a.ExistingID != "" {
				if _, ok := existing[a.ExistingID]; !ok {
					violations = append(violations, Violation{i, a, "unknown_existing_id", "no requirement " + a.ExistingID})
				}
			}
		case ActionMerge, ActionSupersede:
			if a.ExistingID == "" {
				violations = append(violations, Violation{i, a, "missing_existing_id", string(a.Action) + " requires existing_id"})
				continue
			}
			req, ok := existing[a.ExistingID]
			if !ok {
				violations = append(violations, Violation{i, a, "unknown_existing_id", "no requirement " + a.ExistingID})
				continue
			}
			if req.Status != intent.StatusActive {
				// Covers both "never re-enable disabled" and never
				// re-touching an already-superseded requirement: a
				// candidate that duplicates or contradicts a disabled or
				// superseded requirement must be dropped, not merged or
				// superseded.
				violations = append(violations, Violation{
					i, a, "target_not_active",
					fmt.Sprintf("%s is %s; candidate should be dropped instead", a.ExistingID, req.Status),
				})
				continue
			}
			if a.Action == ActionSupersede && a.Statement == "" {
				violations = append(violations, Violation{i, a, "missing_statement", "supersede requires a replacement statement"})
			}
		default:
			violations = append(violations, Violation{i, a, "invalid_action", fmt.Sprintf("unknown action %q", a.Action)})
		}
	}
	return violations
}

// Report summarizes what Apply did.
type Report struct {
	Created    []string // new requirement IDs
	Superseded []string // "<old-id> -> <new-id>"
	Merged     []string // existing IDs a candidate was merged into
	Dropped    int
}

// Apply validates actions against store's current requirements and, only if
// none violate a hard rule, applies them: create writes a new active
// requirement; supersede writes a new active requirement and flips the
// existing one's status to superseded (leaving its statement and scope
// untouched); merge and drop write nothing. Apply is all-or-nothing — if
// Validate reports any violation, no action is applied and the violations
// are returned as an error.
func Apply(store *intent.Store, actions []ReconcileAction) (*Report, error) {
	reqs, err := store.ListRequirements()
	if err != nil {
		return nil, fmt.Errorf("intent scan: loading existing requirements: %w", err)
	}
	byID := make(map[string]*intent.Requirement, len(reqs))
	for _, r := range reqs {
		byID[r.ID] = r
	}

	if violations := Validate(byID, actions); len(violations) > 0 {
		return nil, &ValidationError{Violations: violations}
	}

	report := &Report{}
	now := time.Now().UTC()
	for _, a := range actions {
		switch a.Action {
		case ActionCreate:
			req := &intent.Requirement{
				Status:     intent.StatusActive,
				Scope:      a.Scope,
				Hints:      a.Hints,
				Confidence: a.Confidence,
				UserEdited: false,
				Provenance: a.Provenance,
				Body:       a.Body(),
			}
			created, err := store.CreateRequirement(req)
			if err != nil {
				return nil, fmt.Errorf("intent scan: creating requirement: %w", err)
			}
			report.Created = append(report.Created, created.ID)

		case ActionSupersede:
			newReq := &intent.Requirement{
				Status:     intent.StatusActive,
				Scope:      a.Scope,
				Hints:      a.Hints,
				Confidence: a.Confidence,
				UserEdited: false,
				Provenance: a.Provenance,
				Body:       a.Body(),
			}
			created, err := store.CreateRequirement(newReq)
			if err != nil {
				return nil, fmt.Errorf("intent scan: creating superseding requirement: %w", err)
			}

			old := byID[a.ExistingID]
			old.Status = intent.StatusSuperseded
			old.SupersededBy = created.ID
			old.Updated = now
			if err := store.SaveRequirement(old); err != nil {
				return nil, fmt.Errorf("intent scan: superseding %s: %w", old.ID, err)
			}
			report.Superseded = append(report.Superseded, old.ID+" -> "+created.ID)

		case ActionMerge:
			report.Merged = append(report.Merged, a.ExistingID)

		case ActionDrop:
			report.Dropped++
		}
	}
	return report, nil
}

// ValidationError wraps the violations Validate found; Apply returns one of
// these, unmodified, whenever any action would break a hard rule.
type ValidationError struct {
	Violations []Violation
}

func (e *ValidationError) Error() string {
	msg := fmt.Sprintf("intent scan: %d reconcile action(s) violate hard rules:", len(e.Violations))
	for _, v := range e.Violations {
		msg += "\n  - " + v.Error()
	}
	return msg
}
