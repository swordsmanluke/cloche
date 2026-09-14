package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/cloche-dev/cloche/internal/intent/scan"
)

// cmdIntent dispatches the intent-scan workflow's plumbing subcommands.
// These are invoked by the intent-scan host workflow's script steps
// (.cloche/scripts/intent-scan-*.sh), not typically run by hand — the
// user-facing `cloche intent list/show/edit/...` family lands in a later
// slice (docs/plans/2026-09-13-intent-continuity-implementation.md, slice 6)
// and can extend this same dispatch.
func cmdIntent(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(`usage: cloche intent <subcommand> [args]

Subcommands:
  collect-sources    Gather docs/git-log/run material changed since the last
                      scan into --out, advancing .cloche/intent/scan-state.yaml
  apply-reconcile     Apply a reconcile step's reconcile.json to .cloche/intent/,
                      enforcing the scan's hard rules
`)
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "collect-sources":
		cmdIntentCollectSources(args[1:])
	case "apply-reconcile":
		cmdIntentApplyReconcile(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown intent subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdIntentCollectSources(args []string) {
	projectDir, _ := os.Getwd()
	outDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		case "--out":
			if i+1 < len(args) {
				i++
				outDir = args[i]
			}
		}
	}
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "error: --out is required")
		os.Exit(1)
	}

	collection, err := runIntentCollectSources(projectDir, outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if !collection.HasNew() {
		fmt.Println("no new material since the last scan")
		fmt.Println("CLOCHE_RESULT:none")
		return
	}

	fmt.Printf("collected %d doc(s), %d commit(s), %d run(s) into %s\n",
		len(collection.Docs), len(collection.Commits), len(collection.Runs), outDir)
	fmt.Println("CLOCHE_RESULT:success")
}

// runIntentCollectSources loads the project's scan-state cursors, collects
// material that's changed since, writes it to outDir when there is any,
// and persists the advanced cursors either way — so noise-only commits and
// empty run dirs aren't re-walked on the next scan even when this run was
// quiet.
func runIntentCollectSources(projectDir, outDir string) (*scan.Collection, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	store := intent.NewStore(absProjectDir)
	prev, err := store.LoadScanState()
	if err != nil {
		return nil, fmt.Errorf("loading scan state: %w", err)
	}

	collection, err := scan.Collect(absProjectDir, prev, nil)
	if err != nil {
		return nil, err
	}

	if err := store.SaveScanState(collection.NextState(prev)); err != nil {
		return nil, fmt.Errorf("saving scan state: %w", err)
	}

	if collection.HasNew() {
		if err := collection.Write(outDir); err != nil {
			return nil, fmt.Errorf("writing collected sources: %w", err)
		}
	}

	return collection, nil
}

func cmdIntentApplyReconcile(args []string) {
	projectDir, _ := os.Getwd()
	reconcilePath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		case "--reconcile-file":
			if i+1 < len(args) {
				i++
				reconcilePath = args[i]
			}
		}
	}
	if reconcilePath == "" {
		fmt.Fprintln(os.Stderr, "error: --reconcile-file is required")
		os.Exit(1)
	}

	report, err := runIntentApplyReconcile(projectDir, reconcilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Println("CLOCHE_RESULT:fail")
		os.Exit(1)
	}

	fmt.Printf("created %d, superseded %d, merged %d, dropped %d\n",
		len(report.Created), len(report.Superseded), len(report.Merged), report.Dropped)
	fmt.Println("CLOCHE_RESULT:success")
}

// runIntentApplyReconcile reads a reconcile.json produced by the reconcile
// agent step and applies it to the project's .cloche/intent/ store, failing
// closed (no changes at all) if any action violates a hard rule.
func runIntentApplyReconcile(projectDir, reconcilePath string) (*scan.Report, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	data, err := os.ReadFile(reconcilePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", reconcilePath, err)
	}
	actions, err := scan.ParseReconcileActions(data)
	if err != nil {
		return nil, err
	}

	store := intent.NewStore(absProjectDir)
	return scan.Apply(store, actions)
}
