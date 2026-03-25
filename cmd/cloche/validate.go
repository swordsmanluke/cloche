package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
)

func cmdValidate(args []string) {
	var projectDir, workflowFilter string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflowFilter = args[i]
			}
		}
	}

	if projectDir == "" {
		projectDir, _ = os.Getwd()
	} else {
		abs, err := filepath.Abs(projectDir)
		if err == nil {
			projectDir = abs
		}
	}

	errs := validateProject(projectDir, workflowFilter)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		os.Exit(1)
	}

	fmt.Println("All configuration valid.")
}

// validateProject performs all validation checks and returns a list of errors.
func validateProject(projectDir, workflowFilter string) []string {
	clocheDir := filepath.Join(projectDir, ".cloche")

	info, err := os.Stat(clocheDir)
	if err != nil || !info.IsDir() {
		return []string{fmt.Sprintf("%s: .cloche directory not found", projectDir)}
	}

	var errs []string

	// 1. Validate config.toml
	configErrs := validateConfig(projectDir)
	errs = append(errs, configErrs...)

	// 2. Parse and validate workflow files
	workflows, parseErrs := parseAllWorkflowFiles(clocheDir)
	errs = append(errs, parseErrs...)

	// Filter to specific workflow if requested
	if workflowFilter != "" {
		filtered := make(map[string]*workflowFileInfo)
		for key, wfi := range workflows {
			if wfi.workflow.Name == workflowFilter {
				filtered[key] = wfi
			}
		}
		if len(filtered) == 0 {
			errs = append(errs, fmt.Sprintf("workflow %q not found", workflowFilter))
			return errs
		}
		workflows = filtered
	}

	// Validate each workflow
	for _, wfi := range workflows {
		wfErrs := validateWorkflow(wfi, clocheDir)
		errs = append(errs, wfErrs...)
	}

	// 3. Cross-file consistency (only when not filtering)
	if workflowFilter == "" {
		crossErrs := validateCrossFile(projectDir, workflows)
		errs = append(errs, crossErrs...)
		containerErrs := validateCrossContainerIDs(workflows)
		errs = append(errs, containerErrs...)
	}

	return errs
}

type workflowFileInfo struct {
	workflow *domain.Workflow
	file     string // filename relative to .cloche/
}

// parseAllWorkflowFiles parses all .cloche files and returns workflows keyed by name.
func parseAllWorkflowFiles(clocheDir string) (map[string]*workflowFileInfo, []string) {
	entries, err := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", clocheDir, err)}
	}

	workflows := make(map[string]*workflowFileInfo)
	var errs []string

	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			continue
		}

		wfs, err := dsl.ParseAll(string(data))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			continue
		}

		filename := filepath.Base(path)
		for name, wf := range wfs {
			if existing, ok := workflows[name]; ok {
				errs = append(errs, fmt.Sprintf(
					"%s: workflow %q already defined in %s",
					filename, name, existing.file))
				continue
			}
			workflows[name] = &workflowFileInfo{workflow: wf, file: filename}
		}
	}

	return workflows, errs
}

// validateConfig checks that config.toml parses and is valid.
func validateConfig(projectDir string) []string {
	configPath := filepath.Join(projectDir, ".cloche", "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// config.toml is optional — defaults are used
		return nil
	}

	_, err := config.Load(projectDir)
	if err != nil {
		return []string{fmt.Sprintf("config.toml: %v", err)}
	}

	return nil
}

// validateWorkflow validates a single workflow's structure.
func validateWorkflow(wfi *workflowFileInfo, clocheDir string) []string {
	wf := wfi.workflow
	var errs []string

	// Run domain validation (entry step, wiring, orphans, output mappings)
	if err := wf.Validate(); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", wfi.file, err))
	}

	// Location validation
	if err := wf.ValidateLocation(); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", wfi.file, err))
	}

	// Config key warnings (treat as errors for validate command)
	for _, w := range wf.ValidateConfig() {
		errs = append(errs, fmt.Sprintf("%s: %v", wfi.file, w))
	}

	// Terminal coverage: every path must eventually reach done or abort
	termErrs := validateTerminalCoverage(wf, wfi.file)
	errs = append(errs, termErrs...)

	// Prompt and script file references
	refErrs := validateFileReferences(wf, wfi.file, clocheDir)
	errs = append(errs, refErrs...)

	return errs
}

// validateTerminalCoverage checks that every path through the workflow reaches done or abort.
func validateTerminalCoverage(wf *domain.Workflow, filename string) []string {
	if wf.EntryStep == "" {
		return nil // already reported by Validate()
	}

	// Build adjacency: step+result -> list of target steps
	type stepResult struct{ step, result string }
	adj := make(map[stepResult][]string)
	for _, wire := range wf.Wiring {
		sr := stepResult{wire.From, wire.Result}
		adj[sr] = append(adj[sr], wire.To)
	}
	for _, c := range wf.Collects {
		for _, cond := range c.Conditions {
			sr := stepResult{cond.Step, cond.Result}
			adj[sr] = append(adj[sr], c.To)
		}
	}

	// DFS from entry step. A step is "terminal-safe" if all its results lead
	// (directly or transitively) to done/abort. Track visited to handle cycles.
	var errs []string
	// Use memoization: true = reaches terminal, false = doesn't
	memo := make(map[string]*bool)

	var reaches func(stepName string, visiting map[string]bool) bool
	reaches = func(stepName string, visiting map[string]bool) bool {
		if stepName == domain.StepDone || stepName == domain.StepAbort {
			return true
		}
		if _, ok := wf.Steps[stepName]; !ok {
			return false
		}
		if visiting[stepName] {
			// Cycle — optimistically assume the cycle will exit via
			// another branch. The caller checks all results, so if
			// every branch is a dead-end it will still be caught.
			return true
		}
		if v, ok := memo[stepName]; ok {
			return *v
		}

		visiting[stepName] = true
		step := wf.Steps[stepName]
		allReach := true
		for _, result := range step.Results {
			sr := stepResult{stepName, result}
			targets := adj[sr]
			if len(targets) == 0 {
				allReach = false
				continue
			}
			// At least one target must reach terminal
			anyReach := false
			for _, t := range targets {
				if reaches(t, visiting) {
					anyReach = true
					break
				}
			}
			if !anyReach {
				allReach = false
			}
		}
		delete(visiting, stepName)

		memo[stepName] = &allReach
		return allReach
	}

	// Check each step
	for name, step := range wf.Steps {
		for _, result := range step.Results {
			sr := stepResult{name, result}
			targets := adj[sr]
			if len(targets) == 0 {
				continue // unwired results are caught by Validate()
			}
			anyTerminal := false
			for _, t := range targets {
				if reaches(t, make(map[string]bool)) {
					anyTerminal = true
					break
				}
			}
			if !anyTerminal {
				errs = append(errs, fmt.Sprintf(
					"%s: workflow %q: step %q result %q does not reach a terminal (done/abort)",
					filename, wf.Name, name, result))
			}
		}
	}

	return errs
}

// validateFileReferences checks that prompt file() and script run references exist.
func validateFileReferences(wf *domain.Workflow, filename, clocheDir string) []string {
	var errs []string

	for _, step := range wf.Steps {
		// Check prompt file references: file("prompts/foo.md")
		if promptVal, ok := step.Config["prompt"]; ok {
			if ref := extractFileRef(promptVal); ref != "" {
				path := filepath.Join(filepath.Dir(clocheDir), ref)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					errs = append(errs, fmt.Sprintf(
						"%s: workflow %q: step %q references missing file %q",
						filename, wf.Name, step.Name, ref))
				}
			}
		}

		// Check script run references that point to .cloche/scripts/
		if runVal, ok := step.Config["run"]; ok {
			scriptRef := extractScriptRef(runVal)
			if scriptRef != "" {
				path := filepath.Join(filepath.Dir(clocheDir), scriptRef)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					errs = append(errs, fmt.Sprintf(
						"%s: workflow %q: step %q references missing script %q",
						filename, wf.Name, step.Name, scriptRef))
				}
			}
		}
	}

	return errs
}

// extractFileRef extracts the path from file("path") syntax.
func extractFileRef(val string) string {
	if !strings.HasPrefix(val, `file("`) || !strings.HasSuffix(val, `")`) {
		return ""
	}
	return val[6 : len(val)-2]
}

// extractScriptRef extracts .cloche/scripts/ references from run commands.
// Returns the relative path if the run value references a .cloche/scripts/ file.
func extractScriptRef(val string) string {
	// Look for .cloche/scripts/ references in the run command
	// Common patterns: "bash .cloche/scripts/foo.sh" or ".cloche/scripts/foo.sh"
	parts := strings.Fields(val)
	for _, part := range parts {
		if strings.HasPrefix(part, ".cloche/scripts/") {
			return part
		}
	}
	return ""
}

// validateCrossContainerIDs checks that container workflows sharing the same container id
// have consistent configurations. Exactly one of the following must hold for any group:
//
//	(a) all blocks share the exact same config
//	(b) one has full config and the others only declare id (or use the implicit default)
//	(c) all only declare id (no container config beyond the id field)
func validateCrossContainerIDs(workflows map[string]*workflowFileInfo) []string {
	type wfContainerInfo struct {
		wfName    string
		file      string
		config    map[string]string // container.* keys except container.id
		hasConfig bool              // true if any container config key beyond id
	}

	// Group container workflows by their effective container id.
	groups := make(map[string][]*wfContainerInfo)
	for _, wfi := range workflows {
		wf := wfi.workflow
		if wf.Location != domain.LocationContainer {
			continue
		}

		containerID := wf.ContainerID()

		cfg := make(map[string]string)
		for k, v := range wf.Config {
			if k == "container.id" || k == "_location_block" {
				continue
			}
			if strings.HasPrefix(k, "container.") {
				cfg[k] = v
			}
		}

		groups[containerID] = append(groups[containerID], &wfContainerInfo{
			wfName:    wf.Name,
			file:      wfi.file,
			config:    cfg,
			hasConfig: len(cfg) > 0,
		})
	}

	// Sort container ids for deterministic output.
	containerIDs := make([]string, 0, len(groups))
	for id := range groups {
		containerIDs = append(containerIDs, id)
	}
	sort.Strings(containerIDs)

	var errs []string
	for _, containerID := range containerIDs {
		infos := groups[containerID]
		if len(infos) <= 1 {
			continue
		}

		// Collect workflows that have full config (beyond just declaring an id).
		var withConfig []*wfContainerInfo
		for _, info := range infos {
			if info.hasConfig {
				withConfig = append(withConfig, info)
			}
		}

		// Cases (b) and (c): at most one workflow has full config.
		if len(withConfig) <= 1 {
			continue
		}

		// Case (a): multiple have full config — all must be identical.
		first := withConfig[0]
		for _, other := range withConfig[1:] {
			if !containerConfigsEqual(first.config, other.config) {
				errs = append(errs, fmt.Sprintf(
					"%s: workflow %q: container id %q: config conflicts with workflow %q in %s",
					other.file, other.wfName, containerID, first.wfName, first.file))
			}
		}
	}

	sort.Strings(errs)
	return errs
}

func containerConfigsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// validateCrossFile checks that workflows referenced in config exist as files and vice versa.
func validateCrossFile(projectDir string, workflows map[string]*workflowFileInfo) []string {
	configPath := filepath.Join(projectDir, ".cloche", "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil
	}

	// Check that workflow steps (workflow_name references) point to existing workflows
	var errs []string
	workflowNames := make(map[string]bool)
	for name := range workflows {
		workflowNames[name] = true
	}

	for _, wfi := range workflows {
		for _, step := range wfi.workflow.Steps {
			if step.Type == domain.StepTypeWorkflow {
				refName := step.Config["workflow_name"]
				if refName != "" && !workflowNames[refName] {
					errs = append(errs, fmt.Sprintf(
						"%s: workflow %q: step %q references undefined workflow %q",
						wfi.file, wfi.workflow.Name, step.Name, refName))
				}
			}
		}
	}

	// Sort errors for deterministic output
	sort.Strings(errs)

	return errs
}
