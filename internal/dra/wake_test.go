package dra

import (
	"context"
	"github.com/swordsmanluke/cloche/internal/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swordsmanluke/cloche/internal/domain"
)

func agentArgs(t *testing.T, containers map[string]*domain.Workflow, name string) string {
	t.Helper()
	wf, ok := containers[name]
	if !ok {
		t.Fatalf("no container workflow %q in %v", name, containers)
	}
	return wf.Steps["wake"].Config["agent_args"]
}

func TestFirstWakeCreatesSession(t *testing.T) {
	spec := WakeSpec{Name: "auth", RunID: "r1", NewID: "11111111-1111-4111-8111-111111111111"}
	hostWF, containers := spec.Workflows()
	if hostWF.EntryStep != "fresh" || hostWF.Steps["resume"] != nil {
		t.Fatalf("first wake must go straight to a fresh session: %+v", hostWF.EntryStep)
	}
	args := agentArgs(t, containers, "dra-wake-auth-fresh")
	if !strings.Contains(args, "--session-id "+spec.NewID) || strings.Contains(args, "--resume") {
		t.Errorf("args: %s", args)
	}
	id, mode := spec.Outcome([]*domain.StepExecution{{StepName: "fresh", Result: "success"}})
	if id != spec.NewID || mode != ModeCreated {
		t.Errorf("outcome: %s %s", id, mode)
	}
}

func TestSecondWakeResumes(t *testing.T) {
	spec := WakeSpec{Name: "auth", RunID: "r2", ResumeID: "22222222-2222-4222-8222-222222222222", NewID: "33333333-3333-4333-8333-333333333333"}
	hostWF, containers := spec.Workflows()
	if hostWF.EntryStep != "resume" {
		t.Fatalf("entry: %s", hostWF.EntryStep)
	}
	args := agentArgs(t, containers, "dra-wake-auth-resume")
	if !strings.Contains(args, "--resume "+spec.ResumeID) || strings.Contains(args, "--session-id") {
		t.Errorf("args: %s", args)
	}
	id, mode := spec.Outcome([]*domain.StepExecution{{StepName: "resume", Result: "success"}})
	if id != spec.ResumeID || mode != ModeResumed {
		t.Errorf("outcome: %s %s", id, mode)
	}
}

// A lost session: resume fails, the wiring falls back to a fresh session,
// and the new id replaces the stored one.
func TestLostSessionFallsBack(t *testing.T) {
	spec := WakeSpec{Name: "auth", RunID: "r3", ResumeID: "old", NewID: "new"}
	hostWF, containers := spec.Workflows()
	var to string
	for _, w := range hostWF.Wiring {
		if w.From == "resume" && w.Result == "fail" {
			to = w.To
		}
	}
	if to != "fresh" {
		t.Fatalf("resume failure must fall back to fresh, goes to %q", to)
	}
	if !strings.Contains(agentArgs(t, containers, "dra-wake-auth-fresh"), "--session-id new") {
		t.Error("fallback must start the new session id")
	}
	id, mode := spec.Outcome([]*domain.StepExecution{
		{StepName: "resume", Result: "fail"}, {StepName: "fresh", Result: "success"}})
	if id != "new" || mode != ModeFallback {
		t.Errorf("outcome: %s %s", id, mode)
	}
	// Resume failed and nothing else ran: keep the stored id.
	if id, _ := spec.Outcome([]*domain.StepExecution{{StepName: "resume", Result: "fail"}}); id != "old" {
		t.Errorf("id: %s", id)
	}
}

func TestPromptReadsAndUpdatesNotes(t *testing.T) {
	spec := WakeSpec{Name: "auth", RunID: "r4"}
	for _, fresh := range []bool{true, false} {
		p := spec.Prompt(fresh)
		for _, want := range []string{".cloche/dras/auth/notes.md", ".cloche/dras/auth/charter.md", ".cloche/runs/r4/wake-message.md", "Before you stop, update"} {
			if !strings.Contains(p, want) {
				t.Errorf("fresh=%v prompt missing %q:\n%s", fresh, want, p)
			}
		}
		if strings.Index(p, "notes.md") > strings.Index(p, "Before you stop") {
			t.Error("notes must be read before they are updated")
		}
	}
}

func TestContainerWorkflowIsLockedDRA(t *testing.T) {
	_, containers := (WakeSpec{Name: "auth", RunID: "r", NewID: "n"}).Workflows()
	cfg := containers["dra-wake-auth-fresh"].Config
	if cfg["container.profile"] != "dra" || cfg["container.dra"] != "auth" || cfg["container.notes"] != ".cloche/dras/auth/notes.md" {
		t.Errorf("config: %v", cfg)
	}
}

func TestSessionStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	if m, err := LoadSessions(root); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
	if err := UpdateSession(root, "auth", func(s *Session) { s.ID, s.LastRunID = "abc", "run-1" }); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSession(root, "auth", func(s *Session) { s.LastResult = "succeeded" }); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadSessions(root)
	if s := m["auth"]; s.ID != "abc" || s.LastRunID != "run-1" || s.LastResult != "succeeded" {
		t.Errorf("%+v", s)
	}
}

func TestNewSessionIDIsUUIDv4(t *testing.T) {
	a, b := NewSessionID(), NewSessionID()
	if a == b || len(a) != 36 || a[14] != '4' {
		t.Errorf("%s %s", a, b)
	}
}

func TestWriteMessageAndFind(t *testing.T) {
	root := t.TempDir()
	if err := WriteMessage(root, "run-9", "tests failed {{ x }}"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".cloche", "runs", "run-9", MessageFile))
	if !strings.Contains(string(data), "{{ x }}") {
		t.Errorf("%q", data)
	}
	if _, err := Find(root, "auth"); err == nil {
		t.Error("missing DRA must be an error")
	}
	d := filepath.Join(root, ".cloche", "dras", "auth")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "charter.md"), []byte("# A\n"), 0o644)
	if _, err := Find(root, "auth"); err != nil {
		t.Error(err)
	}
}

func TestVolumeNamePerDRAAndProject(t *testing.T) {
	a, b := VolumeName("/p1", "auth"), VolumeName("/p2", "auth")
	if a == b || a == VolumeName("/p1", "perf") || VolumeName("/p1", "auth") != a {
		t.Errorf("%s %s", a, b)
	}
}

// Driving the generated host workflow through the engine: the first wake runs
// only "fresh"; a failing "resume" falls back to "fresh" and the run still
// succeeds.
func TestWakeWorkflowRunsThroughEngine(t *testing.T) {
	run := func(spec WakeSpec, results map[string]string) (ran []string, state domain.RunState) {
		hostWF, _ := spec.Workflows()
		exec := engine.StepExecutorFunc(func(_ context.Context, step *domain.Step) (domain.StepResult, error) {
			ran = append(ran, step.Name)
			return domain.StepResult{Result: results[step.Name]}, nil
		})
		r, err := engine.New(exec).Run(context.Background(), hostWF)
		if err != nil {
			t.Fatal(err)
		}
		return ran, r.State
	}
	ran, state := run(WakeSpec{Name: "auth", RunID: "r", NewID: "n"}, map[string]string{"fresh": "success"})
	if strings.Join(ran, ",") != "fresh" || state != domain.RunStateSucceeded {
		t.Errorf("first wake: %v %s", ran, state)
	}
	ran, state = run(WakeSpec{Name: "auth", RunID: "r", ResumeID: "o", NewID: "n"}, map[string]string{"resume": "success"})
	if strings.Join(ran, ",") != "resume" || state != domain.RunStateSucceeded {
		t.Errorf("second wake: %v %s", ran, state)
	}
	ran, state = run(WakeSpec{Name: "auth", RunID: "r", ResumeID: "o", NewID: "n"}, map[string]string{"resume": "fail", "fresh": "success"})
	if strings.Join(ran, ",") != "resume,fresh" || state != domain.RunStateSucceeded {
		t.Errorf("lost session: %v %s", ran, state)
	}
	_, state = run(WakeSpec{Name: "auth", RunID: "r", ResumeID: "o", NewID: "n"}, map[string]string{"resume": "fail", "fresh": "fail"})
	if state != domain.RunStateFailed {
		t.Errorf("both failing must fail the run: %s", state)
	}
}
