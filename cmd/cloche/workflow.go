package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
)

func cmdWorkflow(args []string) {
	var projectDir, workflowName string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		default:
			if !strings.HasPrefix(args[i], "-") && workflowName == "" {
				workflowName = args[i]
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

	if workflowName == "" {
		listWorkflows(projectDir)
	} else {
		showWorkflow(projectDir, workflowName)
	}
}

type workflowInfo struct {
	name     string
	location domain.WorkflowLocation
}

func discoverWorkflows(projectDir string) ([]workflowInfo, error) {
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, err := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))
	if err != nil {
		return nil, err
	}

	var infos []workflowInfo
	for _, path := range entries {
		base := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		if base == "host.cloche" {
			wfs, err := dsl.ParseAllForHost(string(data))
			if err != nil {
				continue
			}
			for name := range wfs {
				infos = append(infos, workflowInfo{name: name, location: domain.LocationHost})
			}
		} else {
			wf, err := dsl.ParseForContainer(string(data))
			if err != nil {
				continue
			}
			infos = append(infos, workflowInfo{name: wf.Name, location: domain.LocationContainer})
		}
	}

	return infos, nil
}

func listWorkflows(projectDir string) {
	infos, err := discoverWorkflows(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(infos) == 0 {
		fmt.Println("No workflows found.")
		return
	}

	var container, host []string
	for _, info := range infos {
		switch info.location {
		case domain.LocationContainer:
			container = append(container, info.name)
		case domain.LocationHost:
			host = append(host, info.name)
		}
	}

	sort.Strings(container)
	sort.Strings(host)

	if len(container) > 0 {
		fmt.Println("Container workflows:")
		for _, name := range container {
			fmt.Printf("  %s\n", name)
		}
	}

	if len(host) > 0 {
		if len(container) > 0 {
			fmt.Println()
		}
		fmt.Println("Host workflows:")
		for _, name := range host {
			fmt.Printf("  %s\n", name)
		}
	}
}

func loadWorkflow(projectDir, name string) (*domain.Workflow, error) {
	clocheDir := filepath.Join(projectDir, ".cloche")

	// Try container workflow first (named file)
	containerPath := filepath.Join(clocheDir, name+".cloche")
	if data, err := os.ReadFile(containerPath); err == nil {
		wf, err := dsl.ParseForContainer(string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing %s.cloche: %w", name, err)
		}
		return wf, nil
	}

	// Try host workflow
	hostPath := filepath.Join(clocheDir, "host.cloche")
	if data, err := os.ReadFile(hostPath); err == nil {
		wfs, err := dsl.ParseAllForHost(string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing host.cloche: %w", err)
		}
		if wf, ok := wfs[name]; ok {
			return wf, nil
		}
	}

	return nil, fmt.Errorf("workflow %q not found", name)
}

func showWorkflow(projectDir, name string) {
	wf, err := loadWorkflow(projectDir, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(renderWorkflowGraph(wf, isTTY()))
}

// ANSI color codes for wire colorization.
const (
	colorReset   = "\033[0m"
	colorGreen   = "\033[32m"
	colorRed     = "\033[31m"
	colorBlue    = "\033[34m"
	colorYellow  = "\033[33m"
	colorOrange  = "\033[38;5;208m"
	colorMagenta = "\033[35m"
)

var otherResultColors = []string{colorBlue, colorYellow, colorOrange, colorMagenta}

// buildResultColorMap assigns a color to each unique result name in the workflow.
// "success" is always green, "fail"/"failed" always red, others cycle through
// blue, yellow, orange, magenta.
func buildResultColorMap(w *domain.Workflow, order []string) map[string]string {
	colorMap := map[string]string{
		"success": colorGreen,
		"fail":    colorRed,
		"failed":  colorRed,
	}

	otherIdx := 0
	for _, stepName := range order {
		step := w.Steps[stepName]
		for _, r := range step.Results {
			if _, ok := colorMap[r]; ok {
				continue
			}
			colorMap[r] = otherResultColors[otherIdx%len(otherResultColors)]
			otherIdx++
		}
	}

	return colorMap
}

func renderWorkflowGraph(w *domain.Workflow, color bool) string {
	var buf strings.Builder

	// Header
	fmt.Fprintf(&buf, "workflow %q (%s)\n\n", w.Name, w.Location)

	order := bfsOrder(w)
	if len(order) == 0 {
		buf.WriteString("  (no steps)\n")
		return buf.String()
	}

	colorMap := buildResultColorMap(w, order)

	// Compute max step name length for consistent box widths
	nameWidth := 0
	for _, name := range order {
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
	}
	// Minimum width
	if nameWidth < 6 {
		nameWidth = 6
	}

	for i, stepName := range order {
		step := w.Steps[stepName]
		isEntry := stepName == w.EntryStep

		renderStepBox(&buf, step, isEntry, nameWidth)
		groups := groupWiresByDest(w, stepName)
		renderWireGroups(&buf, groups, colorMap, color)

		// Also render any collect conditions originating from this step
		renderCollects(&buf, w, stepName, colorMap, color)

		if i < len(order)-1 {
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

func renderStepBox(buf *strings.Builder, step *domain.Step, isEntry bool, nameWidth int) {
	padded := fmt.Sprintf(" %-*s ", nameWidth, step.Name)
	border := strings.Repeat("─", nameWidth+2)

	suffix := fmt.Sprintf(" %s", step.Type)
	if isEntry {
		suffix += "  ◀ entry"
	}

	fmt.Fprintf(buf, "  ┌%s┐\n", border)
	fmt.Fprintf(buf, "  │%s│%s\n", padded, suffix)
	fmt.Fprintf(buf, "  └%s┘\n", border)
}

type wireGroup struct {
	results []string
	to      string
}

// groupWiresByDest groups wires from a step by destination, preserving order.
// Wires to the same destination are merged into a single group.
func groupWiresByDest(w *domain.Workflow, stepName string) []wireGroup {
	destMap := make(map[string][]string)
	var destOrder []string

	for _, wire := range w.Wiring {
		if wire.From != stepName {
			continue
		}
		if _, seen := destMap[wire.To]; !seen {
			destOrder = append(destOrder, wire.To)
		}
		destMap[wire.To] = append(destMap[wire.To], wire.Result)
	}

	var groups []wireGroup
	for _, dest := range destOrder {
		groups = append(groups, wireGroup{results: destMap[dest], to: dest})
	}
	return groups
}

func renderWireGroups(buf *strings.Builder, groups []wireGroup, colorMap map[string]string, useColor bool) {
	for i, g := range groups {
		isLast := i == len(groups)-1
		branch := "├"
		if isLast {
			branch = "└"
		}

		// Build colored result list
		var resultParts []string
		for _, r := range g.results {
			if useColor {
				resultParts = append(resultParts, colorMap[r]+r+colorReset)
			} else {
				resultParts = append(resultParts, r)
			}
		}
		resultStr := strings.Join(resultParts, ", ")

		// Format destination
		dest := formatDest(g.to, useColor)

		fmt.Fprintf(buf, "    %s── %s ──▶ %s\n", branch, resultStr, dest)
	}
}

func formatDest(to string, useColor bool) string {
	switch to {
	case domain.StepDone:
		if useColor {
			return colorGreen + "DONE" + colorReset
		}
		return "DONE"
	case domain.StepAbort:
		if useColor {
			return colorRed + "ABORT" + colorReset
		}
		return "ABORT"
	default:
		return to
	}
}

// renderCollects renders any collect conditions that reference this step.
func renderCollects(buf *strings.Builder, w *domain.Workflow, stepName string, colorMap map[string]string, useColor bool) {
	for _, c := range w.Collects {
		// Check if this step is part of the collect conditions
		var parts []string
		involves := false
		for _, cond := range c.Conditions {
			label := cond.Step + ":" + cond.Result
			if useColor {
				if clr, ok := colorMap[cond.Result]; ok {
					label = cond.Step + ":" + clr + cond.Result + colorReset
				}
			}
			parts = append(parts, label)
			if cond.Step == stepName {
				involves = true
			}
		}
		// Only render the collect on the last condition step to avoid duplication
		if involves && c.Conditions[len(c.Conditions)-1].Step == stepName {
			dest := formatDest(c.To, useColor)
			fmt.Fprintf(buf, "    collect %s(%s) ──▶ %s\n", c.Mode, strings.Join(parts, ", "), dest)
		}
	}
}

// bfsOrder returns steps in BFS order from the entry step.
// Handles cycles by skipping already-visited nodes.
func bfsOrder(w *domain.Workflow) []string {
	if w.EntryStep == "" {
		return nil
	}

	visited := map[string]bool{w.EntryStep: true}
	var order []string
	queue := []string{w.EntryStep}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		// Collect unique target steps in wire order
		for _, wire := range w.Wiring {
			if wire.From != current {
				continue
			}
			if wire.To == domain.StepDone || wire.To == domain.StepAbort {
				continue
			}
			if visited[wire.To] {
				continue
			}
			visited[wire.To] = true
			queue = append(queue, wire.To)
		}
	}

	return order
}
