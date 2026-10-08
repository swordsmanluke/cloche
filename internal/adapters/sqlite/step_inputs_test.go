package sqlite

import (
	"context"
	"testing"

	"github.com/swordsmanluke/cloche/internal/domain"
)

func TestStepInputs_AttemptsAccumulateInOrder(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	first := &domain.StepInput{RunID: "r1", StepName: "implement", Kind: domain.StepInputPrompt, Content: "original prompt"}
	if err := s.SaveStepInput(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &domain.StepInput{RunID: "r1", StepName: "implement", Kind: domain.StepInputPrompt, Content: "retry prompt"}
	if err := s.SaveStepInput(ctx, second); err != nil {
		t.Fatal(err)
	}
	// A different step and a different run each start at attempt 1.
	other := &domain.StepInput{RunID: "r1", StepName: "test", Kind: domain.StepInputScript, Content: "command: go test"}
	if err := s.SaveStepInput(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveStepInput(ctx, &domain.StepInput{RunID: "r2", StepName: "implement", Kind: domain.StepInputPrompt, Content: "x"}); err != nil {
		t.Fatal(err)
	}

	if first.Attempt != 1 || second.Attempt != 2 || other.Attempt != 1 {
		t.Fatalf("attempts = %d, %d, %d; want 1, 2, 1", first.Attempt, second.Attempt, other.Attempt)
	}
	if first.CreatedAt.IsZero() {
		t.Error("SaveStepInput should set CreatedAt")
	}

	got, err := s.ListStepInputs(ctx, "r1", "implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "original prompt" || got[1].Content != "retry prompt" {
		t.Fatalf("ListStepInputs = %+v", got)
	}
	if got[0].Kind != domain.StepInputPrompt || got[1].Attempt != 2 || got[1].CreatedAt.IsZero() {
		t.Errorf("row fields not round-tripped: %+v", got)
	}

	none, err := s.ListStepInputs(ctx, "r1", "nope")
	if err != nil || len(none) != 0 {
		t.Errorf("unknown step: got %v, %v", none, err)
	}
}

func TestStepInputs_DeletedWithRun(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()
	run := &domain.Run{ID: "r1", WorkflowName: "develop", ProjectDir: "/p"}
	if err := s.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveStepInput(ctx, &domain.StepInput{RunID: "r1", StepName: "implement", Kind: domain.StepInputPrompt, Content: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRun(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListStepInputs(ctx, "r1", "implement")
	if len(got) != 0 {
		t.Errorf("inputs survived DeleteRun: %+v", got)
	}

	// PurgeProject sweeps them too.
	if err := s.CreateRun(ctx, &domain.Run{ID: "r2", WorkflowName: "develop", ProjectDir: "/p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveStepInput(ctx, &domain.StepInput{RunID: "r2", StepName: "implement", Kind: domain.StepInputPrompt, Content: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeProject(ctx, "/p"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListStepInputs(ctx, "r2", "implement")
	if len(got) != 0 {
		t.Errorf("inputs survived PurgeProject: %+v", got)
	}
}
