package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
)

func TestBfsOrder_LinearWorkflow(t *testing.T) {
	wf := &domain.Workflow{
		EntryStep: "a",
		Steps: map[string]*domain.Step{
			"a": {Name: "a", Results: []string{"ok"}},
			"b": {Name: "b", Results: []string{"ok"}},
			"c": {Name: "c", Results: []string{"ok"}},
		},
		Wiring: []domain.Wire{
			{From: "a", Result: "ok", To: "b"},
			{From: "b", Result: "ok", To: "c"},
			{From: "c", Result: "ok", To: "done"},
		},
	}

	order := bfsOrder(wf)
	if len(order) != 3 {
		t.Fatalf("expected 3 steps, got %d: %v", len(order), order)
	}
	if order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("expected [a b c], got %v", order)
	}
}

func TestBfsOrder_WithCycle(t *testing.T) {
	wf := &domain.Workflow{
		EntryStep: "a",
		Steps: map[string]*domain.Step{
			"a": {Name: "a", Results: []string{"ok", "fail"}},
			"b": {Name: "b", Results: []string{"ok", "retry"}},
		},
		Wiring: []domain.Wire{
			{From: "a", Result: "ok", To: "b"},
			{From: "a", Result: "fail", To: "abort"},
			{From: "b", Result: "ok", To: "done"},
			{From: "b", Result: "retry", To: "a"}, // cycle
		},
	}

	order := bfsOrder(wf)
	if len(order) != 2 {
		t.Fatalf("expected 2 steps (cycle skipped), got %d: %v", len(order), order)
	}
	if order[0] != "a" || order[1] != "b" {
		t.Errorf("expected [a b], got %v", order)
	}
}

func TestBfsOrder_EmptyWorkflow(t *testing.T) {
	wf := &domain.Workflow{Steps: map[string]*domain.Step{}}
	order := bfsOrder(wf)
	if len(order) != 0 {
		t.Errorf("expected empty order, got %v", order)
	}
}

func TestGroupWiresByDest_Merging(t *testing.T) {
	wf := &domain.Workflow{
		Wiring: []domain.Wire{
			{From: "fix", Result: "success", To: "test"},
			{From: "fix", Result: "fail", To: "abort"},
			{From: "fix", Result: "give-up", To: "abort"},
		},
	}

	groups := groupWiresByDest(wf, "fix")
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups (test + abort merged), got %d", len(groups))
	}

	// First group: success -> test
	if groups[0].to != "test" || len(groups[0].results) != 1 || groups[0].results[0] != "success" {
		t.Errorf("expected first group to be success->test, got %+v", groups[0])
	}

	// Second group: fail,give-up -> abort (merged)
	if groups[1].to != "abort" || len(groups[1].results) != 2 {
		t.Errorf("expected second group to be fail,give-up->abort, got %+v", groups[1])
	}
	if groups[1].results[0] != "fail" || groups[1].results[1] != "give-up" {
		t.Errorf("expected results [fail give-up], got %v", groups[1].results)
	}
}

func TestGroupWiresByDest_NoWires(t *testing.T) {
	wf := &domain.Workflow{
		Wiring: []domain.Wire{
			{From: "other", Result: "ok", To: "done"},
		},
	}

	groups := groupWiresByDest(wf, "step")
	if len(groups) != 0 {
		t.Errorf("expected no groups, got %d", len(groups))
	}
}

func TestBuildResultColorMap(t *testing.T) {
	wf := &domain.Workflow{
		Steps: map[string]*domain.Step{
			"a": {Name: "a", Results: []string{"success", "fail", "retry", "skip"}},
		},
	}

	colorMap := buildResultColorMap(wf, []string{"a"})

	if colorMap["success"] != colorGreen {
		t.Error("success should be green")
	}
	if colorMap["fail"] != colorRed {
		t.Error("fail should be red")
	}
	if colorMap["retry"] == "" {
		t.Error("retry should have an assigned color")
	}
	if colorMap["skip"] == "" {
		t.Error("skip should have an assigned color")
	}
	if colorMap["retry"] == colorMap["skip"] {
		t.Error("retry and skip should get different colors")
	}
}

func TestRenderWorkflowGraph_NoColor(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "test-wf",
		Location:  domain.LocationContainer,
		EntryStep: "build",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"deploy": {Name: "deploy", Type: domain.StepTypeAgent, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "deploy"},
			{From: "build", Result: "fail", To: "abort"},
			{From: "deploy", Result: "success", To: "done"},
		},
	}

	output := renderWorkflowGraph(wf, false)

	// Check header
	if !strings.Contains(output, `workflow "test-wf" (container)`) {
		t.Error("missing workflow header")
	}

	// Check step boxes
	if !strings.Contains(output, "build") {
		t.Error("missing build step")
	}
	if !strings.Contains(output, "deploy") {
		t.Error("missing deploy step")
	}

	// Check box drawing characters
	if !strings.Contains(output, "┌") || !strings.Contains(output, "┘") {
		t.Error("missing box drawing characters")
	}

	// Check entry marker
	if !strings.Contains(output, "◀ entry") {
		t.Error("missing entry marker")
	}

	// Check step types
	if !strings.Contains(output, "script") {
		t.Error("missing step type 'script'")
	}
	if !strings.Contains(output, "agent") {
		t.Error("missing step type 'agent'")
	}

	// Check wires
	if !strings.Contains(output, "success ──▶ deploy") {
		t.Error("missing success wire to deploy")
	}
	if !strings.Contains(output, "fail ──▶ ABORT") {
		t.Error("missing fail wire to ABORT")
	}
	if !strings.Contains(output, "success ──▶ DONE") {
		t.Error("missing success wire to DONE")
	}

	// Check tree branch characters
	if !strings.Contains(output, "├──") {
		t.Error("missing non-last branch character")
	}
	if !strings.Contains(output, "└──") {
		t.Error("missing last branch character")
	}
}

func TestRenderWorkflowGraph_WireMerging(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "merge-test",
		Location:  domain.LocationContainer,
		EntryStep: "step1",
		Steps: map[string]*domain.Step{
			"step1": {Name: "step1", Type: domain.StepTypeScript, Results: []string{"success", "fail", "error"}},
		},
		Wiring: []domain.Wire{
			{From: "step1", Result: "success", To: "done"},
			{From: "step1", Result: "fail", To: "abort"},
			{From: "step1", Result: "error", To: "abort"},
		},
	}

	output := renderWorkflowGraph(wf, false)

	// fail and error should be merged into one wire to ABORT
	if !strings.Contains(output, "fail, error ──▶ ABORT") {
		t.Errorf("expected merged wire 'fail, error ──▶ ABORT', got:\n%s", output)
	}

	// Should have exactly 2 wire lines (done + merged abort)
	lines := strings.Split(output, "\n")
	wireLines := 0
	for _, line := range lines {
		if strings.Contains(line, "──▶") {
			wireLines++
		}
	}
	if wireLines != 2 {
		t.Errorf("expected 2 wire lines (1 done + 1 merged abort), got %d", wireLines)
	}
}

func TestRenderWorkflowGraph_WithColor(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "color-test",
		Location:  domain.LocationHost,
		EntryStep: "step1",
		Steps: map[string]*domain.Step{
			"step1": {Name: "step1", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "step1", Result: "success", To: "done"},
			{From: "step1", Result: "fail", To: "abort"},
		},
	}

	output := renderWorkflowGraph(wf, true)

	// Check ANSI color codes are present
	if !strings.Contains(output, colorGreen) {
		t.Error("expected green color code for success")
	}
	if !strings.Contains(output, colorRed) {
		t.Error("expected red color code for fail")
	}
	if !strings.Contains(output, colorReset) {
		t.Error("expected color reset code")
	}
}

func TestRenderWorkflowGraph_HostLocation(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "host-wf",
		Location:  domain.LocationHost,
		EntryStep: "run",
		Steps: map[string]*domain.Step{
			"run": {Name: "run", Type: domain.StepTypeWorkflow, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "run", Result: "success", To: "done"},
		},
	}

	output := renderWorkflowGraph(wf, false)
	if !strings.Contains(output, "(host)") {
		t.Error("expected host location in header")
	}
	if !strings.Contains(output, "workflow") {
		t.Error("expected workflow step type")
	}
}

func TestRenderWorkflowGraph_OtherResultColors(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "multi-color",
		Location:  domain.LocationContainer,
		EntryStep: "step1",
		Steps: map[string]*domain.Step{
			"step1": {Name: "step1", Type: domain.StepTypeAgent, Results: []string{"retry", "skip", "timeout", "partial"}},
		},
		Wiring: []domain.Wire{
			{From: "step1", Result: "retry", To: "done"},
			{From: "step1", Result: "skip", To: "done"},
			{From: "step1", Result: "timeout", To: "abort"},
			{From: "step1", Result: "partial", To: "abort"},
		},
	}

	output := renderWorkflowGraph(wf, true)

	// Each "other" result should get a distinct color from the palette
	if !strings.Contains(output, colorBlue) {
		t.Error("expected blue color for first other result")
	}
	if !strings.Contains(output, colorYellow) {
		t.Error("expected yellow color for second other result")
	}
	if !strings.Contains(output, colorOrange) {
		t.Error("expected orange color for third other result")
	}
	if !strings.Contains(output, colorMagenta) {
		t.Error("expected magenta color for fourth other result")
	}
}

func TestRenderWorkflowGraph_ConsistentBoxWidth(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "width-test",
		Location:  domain.LocationContainer,
		EntryStep: "a",
		Steps: map[string]*domain.Step{
			"a":             {Name: "a", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"long-step-name": {Name: "long-step-name", Type: domain.StepTypeAgent, Results: []string{"ok"}},
		},
		Wiring: []domain.Wire{
			{From: "a", Result: "ok", To: "long-step-name"},
			{From: "long-step-name", Result: "ok", To: "done"},
		},
	}

	output := renderWorkflowGraph(wf, false)
	lines := strings.Split(output, "\n")

	// Find border lines (those with ┌ or └)
	var borderLengths []int
	for _, line := range lines {
		if strings.Contains(line, "┌") && strings.Contains(line, "┐") {
			borderLengths = append(borderLengths, len(line))
		}
	}

	if len(borderLengths) < 2 {
		t.Fatal("expected at least 2 border lines")
	}

	// All borders should have the same length
	for i := 1; i < len(borderLengths); i++ {
		if borderLengths[i] != borderLengths[0] {
			t.Errorf("inconsistent box widths: %d vs %d", borderLengths[0], borderLengths[i])
		}
	}
}

func TestDiscoverWorkflows(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	// Write a container workflow
	os.WriteFile(filepath.Join(clocheDir, "build.cloche"), []byte(`workflow "build" {
  step compile {
    run = "make"
    results = [success, fail]
  }
  compile:success -> done
  compile:fail -> abort
}`), 0644)

	// Write a host workflow with two workflows
	os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(`workflow "plan" {
  step pick {
    run = "echo pick"
    results = [success]
  }
  pick:success -> done
}

workflow "run" {
  step exec {
    run = "echo run"
    results = [success]
  }
  exec:success -> done
}`), 0644)

	infos, err := discoverWorkflows(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should find 3 workflows: 1 container + 2 host
	if len(infos) != 3 {
		t.Fatalf("expected 3 workflows, got %d", len(infos))
	}

	var containerCount, hostCount int
	names := map[string]bool{}
	for _, info := range infos {
		names[info.name] = true
		switch info.location {
		case domain.LocationContainer:
			containerCount++
		case domain.LocationHost:
			hostCount++
		}
	}

	if containerCount != 1 {
		t.Errorf("expected 1 container workflow, got %d", containerCount)
	}
	if hostCount != 2 {
		t.Errorf("expected 2 host workflows, got %d", hostCount)
	}
	for _, name := range []string{"build", "plan", "run"} {
		if !names[name] {
			t.Errorf("missing workflow %q", name)
		}
	}
}

func TestDiscoverWorkflows_NoClocheDir(t *testing.T) {
	dir := t.TempDir()

	infos, err := discoverWorkflows(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(infos))
	}
}

func TestLoadWorkflow_Container(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "deploy.cloche"), []byte(`workflow "deploy" {
  step ship {
    run = "echo ship"
    results = [success, fail]
  }
  ship:success -> done
  ship:fail -> abort
}`), 0644)

	wf, err := loadWorkflow(dir, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "deploy" {
		t.Errorf("expected name 'deploy', got %q", wf.Name)
	}
	if wf.Location != domain.LocationContainer {
		t.Errorf("expected container location, got %q", wf.Location)
	}
}

func TestLoadWorkflow_Host(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(`workflow "orchestrate" {
  step dispatch {
    run = "echo go"
    results = [success]
  }
  dispatch:success -> done
}`), 0644)

	wf, err := loadWorkflow(dir, "orchestrate")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "orchestrate" {
		t.Errorf("expected name 'orchestrate', got %q", wf.Name)
	}
	if wf.Location != domain.LocationHost {
		t.Errorf("expected host location, got %q", wf.Location)
	}
}

func TestLoadWorkflow_NotFound(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)

	_, err := loadWorkflow(dir, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestRenderWorkflowGraph_DevelopWorkflow(t *testing.T) {
	// Simulate the develop workflow from the project
	wf := &domain.Workflow{
		Name:      "develop",
		Location:  domain.LocationContainer,
		EntryStep: "implement",
		Steps: map[string]*domain.Step{
			"implement":   {Name: "implement", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
			"test":        {Name: "test", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"fix":         {Name: "fix", Type: domain.StepTypeAgent, Results: []string{"success", "fail", "give-up"}},
			"update-docs": {Name: "update-docs", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "implement", Result: "success", To: "test"},
			{From: "implement", Result: "fail", To: "abort"},
			{From: "test", Result: "success", To: "update-docs"},
			{From: "test", Result: "fail", To: "fix"},
			{From: "fix", Result: "success", To: "test"},
			{From: "fix", Result: "fail", To: "abort"},
			{From: "fix", Result: "give-up", To: "abort"},
			{From: "update-docs", Result: "success", To: "done"},
			{From: "update-docs", Result: "fail", To: "done"},
		},
	}

	output := renderWorkflowGraph(wf, false)

	// Check BFS order: implement -> test -> update-docs -> fix
	// (update-docs before fix because test:success wire comes before test:fail wire)
	lines := strings.Split(output, "\n")
	stepBoxOrder := []string{}
	for _, line := range lines {
		if strings.Contains(line, "│") && strings.Contains(line, "◀ entry") {
			stepBoxOrder = append(stepBoxOrder, "implement")
		} else if strings.Contains(line, "│") && strings.Contains(line, "script") && strings.Contains(line, "test") {
			stepBoxOrder = append(stepBoxOrder, "test")
		} else if strings.Contains(line, "│") && strings.Contains(line, "update-docs") {
			stepBoxOrder = append(stepBoxOrder, "update-docs")
		} else if strings.Contains(line, "│") && strings.Contains(line, "fix") && strings.Contains(line, "agent") && !strings.Contains(line, "update") && !strings.Contains(line, "implement") {
			stepBoxOrder = append(stepBoxOrder, "fix")
		}
	}

	expected := []string{"implement", "test", "update-docs", "fix"}
	if len(stepBoxOrder) != len(expected) {
		t.Fatalf("expected %d step boxes, got %d: %v", len(expected), len(stepBoxOrder), stepBoxOrder)
	}
	for i, name := range expected {
		if stepBoxOrder[i] != name {
			t.Errorf("step %d: expected %q, got %q (order: %v)", i, name, stepBoxOrder[i], stepBoxOrder)
		}
	}

	// Check wire merging: fix's fail and give-up should merge to ABORT
	if !strings.Contains(output, "fail, give-up ──▶ ABORT") {
		t.Errorf("expected merged wire 'fail, give-up ──▶ ABORT' in output:\n%s", output)
	}

	// Check wire merging: update-docs success and fail should merge to DONE
	if !strings.Contains(output, "success, fail ──▶ DONE") {
		t.Errorf("expected merged wire 'success, fail ──▶ DONE' in output:\n%s", output)
	}

	// Check cycle reference: fix:success -> test (back reference)
	if !strings.Contains(output, "success ──▶ test") {
		t.Error("expected fix:success wire to test")
	}
}

func TestFormatDest(t *testing.T) {
	tests := []struct {
		to       string
		color    bool
		expected string
	}{
		{"done", false, "DONE"},
		{"abort", false, "ABORT"},
		{"step_a", false, "step_a"},
		{"done", true, colorGreen + "DONE" + colorReset},
		{"abort", true, colorRed + "ABORT" + colorReset},
		{"step_a", true, "step_a"},
	}

	for _, tt := range tests {
		got := formatDest(tt.to, tt.color)
		if got != tt.expected {
			t.Errorf("formatDest(%q, %v) = %q, want %q", tt.to, tt.color, got, tt.expected)
		}
	}
}

func TestRenderWorkflowGraph_CollectBlock(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "collect-test",
		Location:  domain.LocationContainer,
		EntryStep: "step1",
		Steps: map[string]*domain.Step{
			"step1": {Name: "step1", Type: domain.StepTypeScript, Results: []string{"success"}},
			"step2": {Name: "step2", Type: domain.StepTypeScript, Results: []string{"success"}},
			"step3": {Name: "step3", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "step1", Result: "success", To: "step2"},
			{From: "step1", Result: "success", To: "step3"},
			{From: "step3", Result: "success", To: "done"},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAll,
				Conditions: []domain.WireCondition{
					{Step: "step2", Result: "success"},
					{Step: "step3", Result: "success"},
				},
				To: "done",
			},
		},
	}

	output := renderWorkflowGraph(wf, false)
	if !strings.Contains(output, "collect all") {
		t.Errorf("expected collect block in output:\n%s", output)
	}
}
