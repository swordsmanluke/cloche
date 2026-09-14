package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/cloche-dev/cloche/internal/intent/scan"
)

func TestRunIntentCollectSources_QuietThenNew(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")

	c, err := runIntentCollectSources(dir, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HasNew() {
		t.Fatalf("expected a quiet scan on an empty project")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("expected no output dir written for a quiet scan")
	}

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	c2, err := runIntentCollectSources(dir, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c2.HasNew() {
		t.Fatalf("expected new material after adding CLAUDE.md")
	}
	if _, err := os.Stat(filepath.Join(out, "docs", "CLAUDE.md")); err != nil {
		t.Fatalf("expected collected doc written to out dir: %v", err)
	}

	// Cursor should have advanced so a third run is quiet again.
	c3, err := runIntentCollectSources(dir, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c3.HasNew() {
		t.Fatalf("expected the scan-state cursor to have advanced past CLAUDE.md")
	}
}

func TestRunIntentApplyReconcile(t *testing.T) {
	dir := t.TempDir()
	reconcile := []map[string]any{
		{
			"action":     "create",
			"statement":  "Never bump major without asking.",
			"confidence": "high",
			"scope":      map[string]any{"level": "project"},
			"provenance": map[string]any{"kind": "doc", "ref": "CLAUDE.md"},
		},
	}
	data, err := json.Marshal(map[string]any{"actions": reconcile})
	if err != nil {
		t.Fatal(err)
	}
	reconcilePath := filepath.Join(dir, "reconcile.json")
	if err := os.WriteFile(reconcilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	report, err := runIntentApplyReconcile(dir, reconcilePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Created) != 1 {
		t.Fatalf("expected 1 created requirement, got %d", len(report.Created))
	}

	if _, err := os.Stat(filepath.Join(dir, ".cloche", "intent", "requirements", report.Created[0]+".md")); err != nil {
		t.Fatalf("expected requirement file written: %v", err)
	}
}

func TestRunIntentApplyReconcile_HardRuleViolationAppliesNothing(t *testing.T) {
	dir := t.TempDir()
	data, err := json.Marshal(map[string]any{
		"actions": []map[string]any{{"action": "delete", "existing_id": "req-0000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcilePath := filepath.Join(dir, "reconcile.json")
	if err := os.WriteFile(reconcilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := runIntentApplyReconcile(dir, reconcilePath); err == nil {
		t.Fatalf("expected an error for an invalid action")
	}
}

// TestIntentScan_EndToEnd_FixtureProject drives the full collect-sources ->
// (stubbed extract/reconcile agent output) -> apply-reconcile pipeline
// against a fixture project, covering create, merge, supersede, and drop in
// one reconcile batch, plus the hard rules a real reconcile agent must obey.
func TestIntentScan_EndToEnd_FixtureProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Project rules\n\nNever bump major without asking.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	store := intent.NewStore(dir)
	dupTarget, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Already-tracked requirement.",
	})
	if err != nil {
		t.Fatal(err)
	}
	editedTarget, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"versioning"}},
		Confidence: intent.ConfidenceHigh,
		UserEdited: true,
		Body:       "The human's carefully chosen wording.",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledTarget, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusDisabled,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceMedium,
		Body:       "A requirement the user turned off.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Step: collect-sources (real, deterministic).
	sourcesOut := filepath.Join(dir, "sources")
	collection, err := runIntentCollectSources(dir, sourcesOut)
	if err != nil {
		t.Fatalf("collect-sources: %v", err)
	}
	if !collection.HasNew() {
		t.Fatalf("expected CLAUDE.md to be picked up as new material")
	}
	if _, err := os.Stat(filepath.Join(sourcesOut, "docs", "CLAUDE.md")); err != nil {
		t.Fatalf("expected collected doc: %v", err)
	}

	// Step: extract (stubbed agent) — not exercised via Apply, but a real
	// candidates.json is written to prove the shape the reconcile step reads
	// matches what MarshalCandidates/ParseCandidates round-trip.
	candidatesPath := filepath.Join(dir, "candidates.json")
	if err := os.WriteFile(candidatesPath, []byte(`{"candidates": [
		{"statement": "Never bump major without asking.", "rationale": "batched by the maintainer", "scope": {"level": "project"}, "confidence": "high", "provenance": {"kind": "doc", "ref": "CLAUDE.md"}}
	]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(candidatesPath); err != nil {
		t.Fatal(err)
	} else if _, err := scan.ParseCandidates(data); err != nil {
		t.Fatalf("candidates.json produced by the stubbed extract step must parse: %v", err)
	}

	// Step: reconcile (stubbed agent) — one action of each kind, covering
	// create, merge into an active requirement, supersede of a user_edited
	// requirement, and drop of a candidate duplicating a disabled one.
	reconcileJSON := map[string]any{
		"actions": []map[string]any{
			{
				"action":     "create",
				"statement":  "Never bump major without asking.",
				"confidence": "high",
				"scope":      map[string]any{"level": "project"},
				"provenance": map[string]any{"kind": "doc", "ref": "CLAUDE.md"},
			},
			{
				"action":      "merge",
				"existing_id": dupTarget.ID,
				"reason":      "already tracked",
			},
			{
				"action":      "supersede",
				"existing_id": editedTarget.ID,
				"statement":   "The machine's replacement wording.",
				"confidence":  "medium",
				"scope":       map[string]any{"level": "domain", "domains": []string{"versioning"}},
				"provenance":  map[string]any{"kind": "commit", "ref": "abc123"},
				"reason":      "newer commit reverses this",
			},
			{
				"action":      "drop",
				"existing_id": disabledTarget.ID,
				"reason":      "duplicates a disabled requirement",
			},
		},
	}
	data, err := json.Marshal(reconcileJSON)
	if err != nil {
		t.Fatal(err)
	}
	reconcilePath := filepath.Join(dir, "reconcile.json")
	if err := os.WriteFile(reconcilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Step: apply-reconcile (real).
	report, err := runIntentApplyReconcile(dir, reconcilePath)
	if err != nil {
		t.Fatalf("apply-reconcile: %v", err)
	}

	if len(report.Created) != 1 {
		t.Fatalf("expected 1 created requirement from the create action, got %d", len(report.Created))
	}
	if len(report.Superseded) != 1 {
		t.Fatalf("expected 1 superseded requirement (its replacement is reported via Superseded, not Created), got %d", len(report.Superseded))
	}
	if len(report.Merged) != 1 || report.Merged[0] != dupTarget.ID {
		t.Fatalf("expected merge into %s, got %v", dupTarget.ID, report.Merged)
	}
	if report.Dropped != 1 {
		t.Fatalf("expected 1 dropped candidate, got %d", report.Dropped)
	}

	reloadedEdited, err := store.GetRequirement(editedTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedEdited.Status != intent.StatusSuperseded {
		t.Fatalf("expected %s to be superseded, got %s", editedTarget.ID, reloadedEdited.Status)
	}
	if reloadedEdited.Body != "The human's carefully chosen wording." {
		t.Fatalf("user_edited requirement's body must survive untouched, got %q", reloadedEdited.Body)
	}

	reloadedDup, err := store.GetRequirement(dupTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedDup.Status != intent.StatusActive {
		t.Fatalf("merge target must remain active, got %s", reloadedDup.Status)
	}

	reloadedDisabled, err := store.GetRequirement(disabledTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedDisabled.Status != intent.StatusDisabled {
		t.Fatalf("disabled requirement must never be re-enabled, got %s", reloadedDisabled.Status)
	}

	// Re-running collect-sources is now quiet (the "re-running a quiet scan
	// is a no-op" acceptance criterion).
	collection2, err := runIntentCollectSources(dir, sourcesOut)
	if err != nil {
		t.Fatalf("second collect-sources: %v", err)
	}
	if collection2.HasNew() {
		t.Fatalf("expected the second scan to be quiet")
	}
}
