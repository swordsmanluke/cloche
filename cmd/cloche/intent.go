package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dsl"
	"github.com/swordsmanluke/cloche/internal/intent"
	"github.com/swordsmanluke/cloche/internal/intent/embed"
	"github.com/swordsmanluke/cloche/internal/intent/scan"
	"github.com/swordsmanluke/cloche/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// cmdIntent dispatches "cloche intent <subcommand>". Most subcommands are
// pure local file mutations against the project's intent.Store — no daemon
// connection needed, per the design ("gRPC only where the daemon owns
// state"). "scan" dials the daemon itself to dispatch the intent-scan
// workflow, the same way cmdRun does. "collect-sources" and
// "apply-reconcile" are plumbing invoked by the intent-scan host workflow's
// script steps (.cloche/scripts/intent-scan-*.sh), not typically run by hand.
func cmdIntent(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(`usage: cloche intent <subcommand> [args]

Subcommands:
  list                List requirements
  show <id>           Show a requirement
  edit <id>           Edit a requirement in $EDITOR
  disable <id>        Disable a requirement
  enable <id>         Enable a requirement
  add <statement>     Add a requirement manually
  preview             Preview the intent block for a workflow/step/prompt
  scan [--full]       Dispatch the intent-scan host workflow. --full resets
                      collection cursors for this run (docs/commits/runs are
                      re-mined from scratch; existing requirements are kept,
                      not discarded) and prints what it's about to re-mine
                      before dispatching
  collect-sources     Gather docs/git-log/run material changed since the last
                      scan into --out, advancing .cloche/intent/scan-state.yaml.
                      --full resets the cursors for this run instead of
                      reading them from scan-state.yaml
  apply-reconcile     Apply a reconcile step's reconcile.json to .cloche/intent/,
                      enforcing the scan's hard rules. --candidates-file lets a
                      missing reconcile.json be treated as a no-op success when
                      candidates.json has zero candidates, instead of failing
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
		if err := intentCommand(args[0], args[1:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

// intentCommand executes one intent subcommand, writing output to w. Split
// out from cmdIntent so it can be exercised in tests without os.Exit.
func intentCommand(sub string, args []string, w io.Writer) error {
	switch sub {
	case "list":
		return intentListCommand(args, w)
	case "show":
		return intentShowCommand(args, w)
	case "edit":
		return intentEditCommand(args, w)
	case "disable":
		return intentSetStatusCommand(args, w, intent.StatusDisabled)
	case "enable":
		return intentSetStatusCommand(args, w, intent.StatusActive)
	case "add":
		return intentAddCommand(args, w)
	case "preview":
		return intentPreviewCommand(args, w)
	case "scan":
		return intentScanCommand(args, w)
	default:
		return fmt.Errorf("unknown intent subcommand: %s", sub)
	}
}

// resolveProjectDir returns dir made absolute, or the current working
// directory if dir is empty.
func resolveProjectDir(dir string) string {
	if dir == "" {
		cwd, _ := os.Getwd()
		return cwd
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// parseProjectDirAndID parses a "[flags] <id>" argument list shared by show,
// edit, disable, and enable.
func parseProjectDirAndID(args []string, usage string) (projectDir, id string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		default:
			if id == "" && !strings.HasPrefix(args[i], "-") {
				id = args[i]
			}
		}
	}
	if id == "" {
		return "", "", fmt.Errorf("usage: cloche intent %s <id> [--project <dir>]", usage)
	}
	return resolveProjectDir(projectDir), id, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func scopeLabel(s intent.Scope) string {
	if s.Level == intent.ScopeLevelProject {
		return "project"
	}
	return strings.Join(s.Domains, ",")
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// intentListCommand implements "cloche intent list [--domain D] [--status S]".
func intentListCommand(args []string, w io.Writer) error {
	var domainFilter, statusFilter, projectDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--domain":
			if i+1 < len(args) {
				i++
				domainFilter = args[i]
			}
		case "--status":
			if i+1 < len(args) {
				i++
				statusFilter = args[i]
			}
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		}
	}

	store := intent.NewStore(resolveProjectDir(projectDir))
	reqs, err := store.ListRequirements()
	if err != nil {
		return err
	}

	var filtered []*intent.Requirement
	for _, r := range reqs {
		if statusFilter != "" && string(r.Status) != statusFilter {
			continue
		}
		if domainFilter != "" && !containsString(r.Scope.Domains, domainFilter) {
			continue
		}
		filtered = append(filtered, r)
	}

	if len(filtered) == 0 {
		fmt.Fprintln(w, "No requirements found.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tSCOPE\tSOURCE\tSTATEMENT")
	for _, r := range filtered {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Status, scopeLabel(r.Scope), r.Provenance.Kind, truncateRunes(firstLine(r.Body), 60))
	}
	return tw.Flush()
}

// intentShowCommand implements "cloche intent show <id>".
func intentShowCommand(args []string, w io.Writer) error {
	projectDir, id, err := parseProjectDirAndID(args, "show")
	if err != nil {
		return err
	}

	store := intent.NewStore(projectDir)
	req, err := store.GetRequirement(id)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "ID:          %s\n", req.ID)
	fmt.Fprintf(w, "Status:      %s\n", req.Status)
	if req.Status == intent.StatusSuperseded && req.SupersededBy != "" {
		fmt.Fprintf(w, "Superseded by: %s\n", req.SupersededBy)
	}
	fmt.Fprintf(w, "Scope:       %s\n", scopeLabel(req.Scope))
	if len(req.Scope.Paths) > 0 {
		fmt.Fprintf(w, "Paths:       %s\n", strings.Join(req.Scope.Paths, ", "))
	}
	if len(req.Scope.Languages) > 0 {
		fmt.Fprintf(w, "Languages:   %s\n", strings.Join(req.Scope.Languages, ", "))
	}
	fmt.Fprintf(w, "Confidence:  %s\n", req.Confidence)
	fmt.Fprintf(w, "User edited: %v\n", req.UserEdited)
	fmt.Fprintf(w, "Provenance:  %s (%s)\n", req.Provenance.Kind, req.Provenance.Ref)
	if !req.Provenance.ExtractedAt.IsZero() {
		fmt.Fprintf(w, "Extracted:   %s by %s\n", req.Provenance.ExtractedAt.Format(time.RFC3339), req.Provenance.ExtractedBy)
	}
	fmt.Fprintf(w, "Created:     %s\n", req.Created.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:     %s\n", req.Updated.Format(time.RFC3339))
	if len(req.Hints) > 0 {
		fmt.Fprintln(w, "Hints:")
		for _, h := range req.Hints {
			fmt.Fprintf(w, "  - %s\n", h)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, req.Body)
	return nil
}

// resolveEditor returns the command used to open a file for interactive
// editing: $EDITOR, then $VISUAL, then "vi".
func resolveEditor() string {
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	return "vi"
}

// openInEditor runs the resolved editor on path, connected to the current
// process's stdio so the user can interact with it. Run through a shell so
// multi-word editor commands (e.g. "code --wait") work.
func openInEditor(path string) error {
	cmd := exec.Command("sh", "-c", resolveEditor()+` "$1"`, "--", path) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// intentEditCommand implements "cloche intent edit <id>": opens $EDITOR on
// the requirement file directly, then re-reads it and stamps
// user_edited=true (regardless of what the editor actually changed — the
// design's contract is that user_edited means "a human touched this file
// through the edit path", not a diff check) before re-saving through the
// store so formatting stays canonical.
func intentEditCommand(args []string, w io.Writer) error {
	projectDir, id, err := parseProjectDirAndID(args, "edit")
	if err != nil {
		return err
	}

	store := intent.NewStore(projectDir)
	if _, err := store.GetRequirement(id); err != nil {
		return err
	}

	path := filepath.Join(store.IntentDir(), "requirements", id+".md")
	if err := openInEditor(path); err != nil {
		return fmt.Errorf("running editor: %w", err)
	}

	req, err := store.GetRequirement(id)
	if err != nil {
		return fmt.Errorf("re-reading edited requirement: %w", err)
	}
	req.UserEdited = true
	req.Updated = time.Now().UTC()
	if err := store.SaveRequirement(req); err != nil {
		return err
	}

	fmt.Fprintf(w, "Updated %s (user_edited=true)\n", req.ID)
	return nil
}

// intentSetStatusCommand implements "cloche intent disable/enable <id>".
func intentSetStatusCommand(args []string, w io.Writer, status intent.Status) error {
	usage := "enable"
	if status == intent.StatusDisabled {
		usage = "disable"
	}
	projectDir, id, err := parseProjectDirAndID(args, usage)
	if err != nil {
		return err
	}

	store := intent.NewStore(projectDir)
	req, err := store.GetRequirement(id)
	if err != nil {
		return err
	}

	if req.Status == status {
		fmt.Fprintf(w, "%s already %s\n", req.ID, status)
		return nil
	}

	req.Status = status
	req.Updated = time.Now().UTC()
	if err := store.SaveRequirement(req); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s -> %s\n", req.ID, status)
	return nil
}

// intentAddCommand implements "cloche intent add <statement> [--domain D]...".
// Manually authored requirements are always provenance kind=user and
// user_edited=true — there's no scan to later "touch" them, so they start
// out as canonical as any edited requirement.
func intentAddCommand(args []string, w io.Writer) error {
	var projectDir, statement string
	var domains []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--domain":
			if i+1 < len(args) {
				i++
				domains = append(domains, args[i])
			}
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		default:
			if statement == "" && !strings.HasPrefix(args[i], "-") {
				statement = args[i]
			}
		}
	}
	if statement == "" {
		return fmt.Errorf("usage: cloche intent add \"statement\" [--domain <name>]... [--project <dir>]")
	}

	scope := intent.Scope{Level: intent.ScopeLevelProject}
	if len(domains) > 0 {
		scope = intent.Scope{Level: intent.ScopeLevelDomain, Domains: domains}
	}

	store := intent.NewStore(resolveProjectDir(projectDir))
	now := time.Now().UTC()
	req := &intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      scope,
		Confidence: intent.ConfidenceHigh,
		UserEdited: true,
		Provenance: intent.Provenance{
			Kind:        intent.ProvenanceUser,
			ExtractedAt: now,
			ExtractedBy: "cloche intent add",
		},
		Body: statement,
	}

	created, err := store.CreateRequirement(req)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Created %s\n", created.ID)
	return nil
}

// splitCommaList splits a comma-separated config value into trimmed,
// non-empty parts.
func splitCommaList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// promptTemplateName derives the retrieval-query "prompt template name" for
// a step, per the design's query composition (task description + step
// prompt template name + workflow name — never the full prompt body). A
// `prompt = file("prompts/foo.md")` config yields "foo"; anything else
// (inline prompt text, or no prompt config at all) falls back to the step
// name, which is still a stable, meaningful token for retrieval.
func promptTemplateName(step *domain.Step) string {
	raw, ok := step.Config["prompt"]
	if !ok {
		return step.Name
	}
	if strings.HasPrefix(raw, `file("`) && strings.HasSuffix(raw, `")`) {
		inner := strings.TrimSuffix(strings.TrimPrefix(raw, `file("`), `")`)
		base := filepath.Base(inner)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return step.Name
}

// resolveRepoPaths maps a workflow's declared `repos = [...]` entries (names
// referring to [[repositories]] entries in config.toml) to their configured
// paths, so they can be matched against domain path globs the same way
// query.Repos is documented and tested to work: as paths, not repo names.
func resolveRepoPaths(cfg *config.Config, repoNames []string) []string {
	if cfg == nil || len(repoNames) == 0 {
		return nil
	}
	byName := make(map[string]string, len(cfg.Repositories))
	for _, r := range cfg.Repositories {
		byName[r.Name] = r.Path
	}
	var paths []string
	for _, name := range repoNames {
		if p, ok := byName[name]; ok && p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// intentInjectionOff reports whether the resolved workflow/step/config opts
// out of intent injection, per the design's opt-outs: a step or workflow
// `intent_tracking = false` config key, or project-wide `intent.inject =
// "off"` in config.toml.
func intentInjectionOff(cfg *config.Config, wf *domain.Workflow, step *domain.Step) bool {
	if step != nil && step.Config["intent_tracking"] == "false" {
		return true
	}
	if wf != nil && wf.Config["intent_tracking"] == "false" {
		return true
	}
	if cfg != nil && cfg.Intent.Inject == "off" {
		return true
	}
	return false
}

// excludedIntentTrackingSteps returns the names of every step, across every
// workflow discovered in the project (host and container), whose step-level
// or workflow-level `intent_tracking = false` config opts it out of
// collect-sources mining. Best-effort: a workflow file that fails to parse
// is skipped rather than failing the scan. Step names are not qualified by
// workflow, so identically named steps in different workflows share
// exclusion.
func excludedIntentTrackingSteps(projectDir string) map[string]bool {
	excluded := map[string]bool{}
	entries, err := filepath.Glob(filepath.Join(projectDir, ".cloche", "*.cloche"))
	if err != nil {
		return excluded
	}
	for _, path := range entries {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		wfs, parseErr := dsl.ParseAll(string(data))
		if parseErr != nil {
			continue
		}
		for _, wf := range wfs {
			wfOff := wf.Config["intent_tracking"] == "false"
			for name, step := range wf.Steps {
				if wfOff || step.Config["intent_tracking"] == "false" {
					excluded[name] = true
				}
			}
		}
	}
	return excluded
}

// intentPreviewCommand implements:
//
//	cloche intent preview [--workflow W] [--step S] [--prompt "..."] [--project DIR]
//
// It renders the exact block intent.Select + intent.FormatBlock would
// produce for the given inputs — the same two calls the daemon-side prompt
// injection makes, so this is a direct preview rather than a
// reimplementation of selection.
func intentPreviewCommand(args []string, w io.Writer) error {
	var projectDir, workflowName, stepName, prompt string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflowName = args[i]
			}
		case "--step":
			if i+1 < len(args) {
				i++
				stepName = args[i]
			}
		case "--prompt", "-p":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		case "--project":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		}
	}
	projectDir = resolveProjectDir(projectDir)

	store := intent.NewStore(projectDir)
	reqs, err := store.ListRequirements()
	if err != nil {
		return err
	}
	domainMap, err := store.LoadDomains()
	if err != nil {
		return err
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		cfg = nil // soft-fail: preview still works without config.toml context
	}

	query := intent.Query{TaskDescription: prompt, WorkflowName: workflowName}
	var wf *domain.Workflow
	var step *domain.Step
	if workflowName != "" {
		if loaded, loadErr := loadWorkflow(projectDir, workflowName); loadErr == nil {
			wf = loaded
			query.Repos = resolveRepoPaths(cfg, wf.Repos)
			if d, ok := wf.Config["domains"]; ok {
				query.Domains = splitCommaList(d)
			}
			if stepName != "" {
				if s, ok := wf.Steps[stepName]; ok {
					step = s
					query.StepPromptName = promptTemplateName(s)
					if d, ok := s.Config["domains"]; ok {
						query.Domains = splitCommaList(d)
					}
				}
			}
		}
	}

	if intentInjectionOff(cfg, wf, step) {
		fmt.Fprintln(w, "(intent injection is off for this workflow/step; no block would be injected)")
		return nil
	}

	ctx := context.Background()
	var embedder embed.Embedder
	var idx *intent.Index
	pin := ""
	if cfg != nil {
		pin = cfg.Intent.Embedder
	}
	if e, resolveErr := embed.Resolve(ctx, pin); resolveErr == nil {
		embedder = e
		if built, idxErr := intent.NewIndex(projectDir, embedder); idxErr == nil {
			items := make([]intent.Item, 0, len(reqs))
			for _, r := range reqs {
				if r.Status != intent.StatusActive {
					continue
				}
				items = append(items, intent.Item{ID: r.ID, Text: intent.EmbedText(r)})
			}
			if syncErr := built.Sync(ctx, items); syncErr == nil {
				idx = built
			}
		}
	}

	var opts intent.Options
	if cfg != nil && cfg.Intent.TokenBudget > 0 {
		opts.TokenBudget = cfg.Intent.TokenBudget
	}

	result, err := intent.Select(ctx, reqs, domainMap.Domains, idx, embedder, query, opts)
	if err != nil {
		return err
	}

	block := intent.FormatBlock(result)
	if block == "" {
		fmt.Fprintln(w, "(no requirements selected)")
		return nil
	}
	fmt.Fprint(w, block)
	return nil
}

// intentScanCommand implements "cloche intent scan [--full]": an alias that
// dispatches the intent-scan host workflow, the same RPC cmdRun uses.
func intentScanCommand(args []string, w io.Writer) error {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	return intentScanWithClient(context.Background(), client, args, w)
}

// intentScanWithClient does the actual RunWorkflow dispatch, split out from
// intentScanCommand so it can be tested against a fake ClocheServiceClient
// without dialing a real daemon. It then waits for the run to finish and
// prints the same collection summary the Requirements view shows, so a
// foreground `cloche intent scan` gives useful feedback instead of just a
// run ID.
func intentScanWithClient(ctx context.Context, client pb.ClocheServiceClient, args []string, w io.Writer) error {
	var full bool
	for _, a := range args {
		if a == "--full" {
			full = true
		}
	}

	cwd, _ := os.Getwd()

	var env map[string]string
	if full {
		// CLOCHE_INTENT_FULL is how --full reaches the workflow's steps: an
		// explicit env var, not the run prompt (nothing reads the prompt for
		// a host workflow run — see runHostWorkflow). collect-sources honors
		// it by resetting its cursors for this run; discover-domains honors
		// it by forcing a full domain survey regardless of whether
		// domains.yaml already exists.
		env = map[string]string{"CLOCHE_INTENT_FULL": "1"}
		if err := printFullRescanPreview(cwd, w); err != nil {
			fmt.Fprintf(w, "(could not preview --full re-mine: %v)\n", err)
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	resp, err := client.RunWorkflow(runCtx, &pb.RunWorkflowRequest{
		WorkflowName: "intent-scan",
		ProjectDir:   cwd,
		Env:          env,
	})
	cancel()
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Started intent scan: %s\n", resp.RunId)
	if resp.TaskId != "" {
		fmt.Fprintf(w, "Task:        %s\n", resp.TaskId)
	}
	if resp.AttemptId != "" {
		fmt.Fprintf(w, "Attempt:     %s\n", resp.AttemptId)
	}

	state, err := waitForIntentScan(ctx, client, resp.RunId, w)
	if err != nil {
		fmt.Fprintf(w, "(could not wait for the scan to finish: %v)\n", err)
		return nil
	}
	if state != "succeeded" {
		fmt.Fprintf(w, "intent scan %s\n", state)
		return nil
	}

	if stats, err := loadScanStats(cwd); err == nil {
		fmt.Fprintln(w, formatScanSummaryLine(stats))
	}
	return nil
}

// waitForIntentScan polls the daemon for runID's status until it reaches a
// terminal state (succeeded, failed, cancelled), printing each step's
// result as it lands, the same way `cloche poll` does.
func waitForIntentScan(ctx context.Context, client pb.ClocheServiceClient, runID string, w io.Writer) (string, error) {
	var lastStepCount int
	for {
		getCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := client.GetStatus(getCtx, &pb.GetStatusRequest{Id: runID})
		cancel()
		if err != nil {
			return "", err
		}

		for i := lastStepCount; i < len(resp.StepExecutions); i++ {
			exec := resp.StepExecutions[i]
			if exec.Result != "" {
				fmt.Fprintf(w, "  %s: %s\n", exec.StepName, exec.Result)
			}
		}
		lastStepCount = len(resp.StepExecutions)

		if isTerminalState(resp.State) {
			return resp.State, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// loadScanStats reads the project's scan-state.yaml and returns the most
// recent collect-sources pass's per-repo stats.
func loadScanStats(projectDir string) (intent.ScanStats, error) {
	absDir, err := filepath.Abs(projectDir)
	if err != nil {
		return intent.ScanStats{}, err
	}
	st, err := intent.NewStore(absDir).LoadScanState()
	if err != nil {
		return intent.ScanStats{}, err
	}
	return st.LastScanStats, nil
}

// formatScanSummaryLine formats a ScanStats as the aggregate summary shown
// by `cloche intent scan` and the Requirements view's meta line: total docs,
// commits, and runs/transcripts collected across however many repos were
// scanned, plus a warning for any configured repo that contributed nothing.
func formatScanSummaryLine(stats intent.ScanStats) string {
	line := fmt.Sprintf("last scan: %d docs · %d commits · %d transcripts",
		stats.TotalDocs(), stats.TotalCommits(), stats.TotalRuns())
	if len(stats.Repos) > 1 {
		line += fmt.Sprintf(" across %d repos", len(stats.Repos))
	}
	if empty := stats.EmptyRepoNames(); len(empty) > 0 {
		line += fmt.Sprintf(" (warning: %s contributed nothing)", strings.Join(empty, ", "))
	}
	return line
}

// printFullRescanPreview reports what a --full scan is about to re-mine,
// computed by collecting against a reset cursor the same way collect-sources
// does when CLOCHE_INTENT_FULL is set. This is a client-side, read-only
// preview — it doesn't persist anything; the dispatched workflow's
// collect-sources step does the real collection and cursor advance. Mirrors
// runIntentCollectSources's repo resolution so the preview accounts for
// every configured [[repositories]] entry, not just the project root.
func printFullRescanPreview(projectDir string, w io.Writer) error {
	cfg, err := config.Load(projectDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	collection, err := scan.Collect(projectDir, cfg, &intent.ScanState{}, nil, excludedIntentTrackingSteps(projectDir))
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "--full: re-mining %d doc(s), %d commit(s), %d run(s) from %s\n",
		len(collection.Docs), len(collection.Commits), len(collection.Runs), projectDir)
	return nil
}

// printResultMarker prints the CLOCHE_RESULT marker for name, framed with
// the step nonce from CLOCHE_RESULT_NONCE when the caller (the host
// executor) set one, so the marker can't be confused with unrelated
// "CLOCHE_RESULT:" text this command's own output may contain (e.g. commits
// or docs it collected that happen to mention the protocol literally).
// Falls back to a bare marker when the env var is unset, for callers that
// invoke this command directly without the nonce protocol.
func printResultMarker(name string) {
	if nonce := os.Getenv("CLOCHE_RESULT_NONCE"); nonce != "" {
		fmt.Println(protocol.FormatNoncedMarker(nonce, name))
		return
	}
	fmt.Println(protocol.ResultPrefix + name)
}

func cmdIntentCollectSources(args []string) {
	projectDir, _ := os.Getwd()
	outDir := ""
	var full bool
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
		case "--full":
			full = true
		}
	}
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "error: --out is required")
		os.Exit(1)
	}

	collection, stats, err := runIntentCollectSources(projectDir, outDir, full)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, r := range stats.Repos {
		fmt.Println(formatRepoStatsLine(r))
	}

	if !collection.HasNew() {
		fmt.Println("no new material since the last scan")
		printResultMarker("none")
		return
	}

	fmt.Printf("collected %d doc(s), %d commit(s), %d run(s) into %s\n",
		len(collection.Docs), len(collection.Commits), len(collection.Runs), outDir)
	printResultMarker("success")
}

// formatRepoStatsLine formats one repo's RepoStats as the per-repo summary
// line collect-sources logs: docs collected (new/changed), commits
// collected (range), runs/transcripts collected, and bytes handed to the
// extract step. The root repo (Name == "") is labeled ".".
func formatRepoStatsLine(r intent.RepoStats) string {
	name := r.Name
	if name == "" {
		name = "."
	}
	commitRange := r.CommitRange
	if commitRange == "" {
		commitRange = "-"
	}
	return fmt.Sprintf("repo %s: %d new doc(s), %d changed doc(s), %d commit(s) (%s), %d run(s)/transcript(s), %d byte(s) handed to extract",
		name, r.DocsNew, r.DocsChanged, r.Commits, commitRange, r.Runs, r.Bytes)
}

// runIntentCollectSources loads the project's scan-state cursors, collects
// material that's changed since (from the project root and any configured
// [[repositories]] entries), writes it to outDir when there is any, and
// persists the advanced cursors and this pass's per-repo stats either way —
// so noise-only commits and empty run dirs aren't re-walked on the next
// scan even when this run was quiet. When full is true, the persisted
// cursors (doc hashes, last commit, scanned runs) are treated as empty for
// this run — re-mining everything collect-sources looks at — without
// touching .cloche/intent/requirements/ at all; reconcile still
// merges/supersedes existing requirements as usual.
func runIntentCollectSources(projectDir, outDir string, full bool) (*scan.Collection, intent.ScanStats, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, intent.ScanStats{}, fmt.Errorf("resolving project dir: %w", err)
	}

	store := intent.NewStore(absProjectDir)
	prev, err := store.LoadScanState()
	if err != nil {
		return nil, intent.ScanStats{}, fmt.Errorf("loading scan state: %w", err)
	}

	cfg, err := config.Load(absProjectDir)
	if err != nil {
		cfg = nil // soft-fail: a project with no config.toml is legacy single-tree
	}

	collectFrom := prev
	if full {
		collectFrom = &intent.ScanState{}
	}

	collection, err := scan.Collect(absProjectDir, cfg, collectFrom, nil, excludedIntentTrackingSteps(absProjectDir))
	if err != nil {
		return nil, intent.ScanStats{}, err
	}

	next := collection.NextState(prev)
	next.LastScanAt = time.Now().UTC()
	next.LastScanStats = collection.Stats

	if err := store.SaveScanState(next); err != nil {
		return nil, intent.ScanStats{}, fmt.Errorf("saving scan state: %w", err)
	}

	if collection.HasNew() {
		if err := collection.Write(outDir); err != nil {
			return nil, intent.ScanStats{}, fmt.Errorf("writing collected sources: %w", err)
		}
	}

	return collection, next.LastScanStats, nil
}

func cmdIntentApplyReconcile(args []string) {
	projectDir, _ := os.Getwd()
	reconcilePath := ""
	candidatesPath := ""
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
		case "--candidates-file":
			if i+1 < len(args) {
				i++
				candidatesPath = args[i]
			}
		}
	}
	if reconcilePath == "" {
		fmt.Fprintln(os.Stderr, "error: --reconcile-file is required")
		os.Exit(1)
	}

	report, err := runIntentApplyReconcile(projectDir, reconcilePath, candidatesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		printResultMarker("fail")
		os.Exit(1)
	}
	if report == nil {
		fmt.Println("no candidates to reconcile, nothing to apply")
		printResultMarker("success")
		return
	}

	fmt.Printf("created %d, superseded %d, merged %d, dropped %d\n",
		len(report.Created), len(report.Superseded), len(report.Merged), report.Dropped)
	printResultMarker("success")
}

// runIntentApplyReconcile reads a reconcile.json produced by the reconcile
// agent step and applies it to the project's .cloche/intent/ store, failing
// closed (no changes at all) if any action violates a hard rule.
//
// A missing reconcile.json is only a hard failure when candidatesPath shows
// there was something to reconcile: the reconcile agent occasionally reports
// success without writing its output (see cloche-26029ae7feb8), and extract
// producing zero candidates is a legitimate reason for reconcile to have
// nothing to write in the first place — that case must return (nil, nil), a
// no-op success, not an error. A missing/unreadable candidatesPath when
// reconcile.json is also missing can't be told apart from "should have
// written something", so it fails closed like before.
func runIntentApplyReconcile(projectDir, reconcilePath, candidatesPath string) (*scan.Report, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	data, err := os.ReadFile(reconcilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading %s: %w", reconcilePath, err)
		}
		hasCandidates, candErr := candidatesFileHasCandidates(candidatesPath)
		if candErr != nil {
			return nil, fmt.Errorf("%s not found, and checking %s: %w", reconcilePath, candidatesPath, candErr)
		}
		if hasCandidates {
			return nil, fmt.Errorf("%s not found but %s has candidates — did the reconcile step write it?", reconcilePath, candidatesPath)
		}
		return nil, nil
	}
	actions, err := scan.ParseReconcileActions(data)
	if err != nil {
		return nil, err
	}

	store := intent.NewStore(absProjectDir)
	return scan.Apply(store, actions)
}

// candidatesFileHasCandidates reports whether the extract step's
// candidates.json (at path) lists at least one candidate.
func candidatesFileHasCandidates(path string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("no --candidates-file given")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	candidates, err := scan.ParseCandidates(data)
	if err != nil {
		return false, err
	}
	return len(candidates) > 0, nil
}
