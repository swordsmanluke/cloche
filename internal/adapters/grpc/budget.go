package grpc

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/budget"
	"github.com/swordsmanluke/cloche/internal/domain"
)

const (
	// budgetParkTitle marks a run parked by the daily budget cap (as opposed
	// to one parked on a help thread, which has a ParkedThreadID).
	budgetParkTitle = "budget"
	// kvBudgetParkStep records the step that was in flight when a run was
	// parked, so `cloche resume` knows where to continue from.
	kvBudgetParkStep = "_budget.park_step"
)

// SetBudget installs the daily budget monitor: the gate for new launches, the
// source of the status line, and the owner of the grace timer, whose expiry
// parks the runs still in flight. nil disables the budget.
func (s *ClocheServer) SetBudget(m *budget.Monitor) {
	s.budget = m
	if m != nil {
		m.SetOnGraceExpired(func() { s.ParkRunsForBudget(context.Background()) })
	}
}

// budgetBlocked reports whether new launches are held back by the cap.
func (s *ClocheServer) budgetBlocked() bool {
	return s.budget != nil && s.budget.Blocked()
}

// GetBudget implements the GetBudget RPC.
func (s *ClocheServer) GetBudget(ctx context.Context, _ *pb.GetBudgetRequest) (*pb.GetBudgetResponse, error) {
	if s.budget == nil {
		return &pb.GetBudgetResponse{}, nil
	}
	st := s.budget.Status()
	resp := &pb.GetBudgetResponse{
		Enabled:      st.Enabled,
		CapPercent:   st.CapPercent,
		UsedPercent:  st.UsedPercent,
		Over:         st.Over,
		GraceExpired: st.GraceExpired,
		Description:  st.Describe(time.Now()),
		ReadError:    st.ReadErr,
	}
	if !st.ClearsAt.IsZero() {
		resp.ClearsAt = st.ClearsAt.Format(time.RFC3339)
	}
	if !st.GraceEndsAt.IsZero() {
		resp.GraceEndsAt = st.GraceEndsAt.Format(time.RFC3339)
	}
	return resp, nil
}

// budgetWarning is the warning attached to a hand-started run while the cap is
// exceeded. Hand-started runs are never blocked.
func (s *ClocheServer) budgetWarning() string {
	if !s.budgetBlocked() {
		return ""
	}
	st := s.budget.Status()
	return "daily budget cap reached: " + st.Describe(time.Now()) + " (starting anyway because this run was started by hand)"
}

// budgetAttentionItem returns the daemon-wide "Needs you" item for the budget:
// the cap being exceeded (naming the cap and when the day's window clears),
// usage being unreadable (the cap is then not enforced), and runs the cap
// parked that still wait for `cloche resume`, which outlive the over-cap state.
func (s *ClocheServer) budgetAttentionItem(ctx context.Context) (attention.Item, bool) {
	if s.budget == nil {
		return attention.Item{}, false
	}
	st := s.budget.Status()
	if !st.Enabled {
		return attention.Item{}, false
	}
	var parts []string
	since := st.OverSince
	switch {
	case st.Over:
		parts = append(parts, st.Describe(time.Now()))
	case st.ReadErr != "":
		parts = append(parts, "cannot read usage, so the daily cap is not enforced: "+st.ReadErr)
		since = st.CheckedAt
	}
	if ids := s.budgetParkedRunIDs(ctx); len(ids) > 0 {
		parts = append(parts, fmt.Sprintf("%d run(s) parked by the cap, continue each with `cloche resume <id>`: %s", len(ids), strings.Join(ids, ", ")))
	}
	if len(parts) == 0 {
		return attention.Item{}, false
	}
	return attention.Item{
		Kind:   attention.KindBudget,
		Reason: strings.Join(parts, "; "),
		Since:  since,
		Key:    "budget",
	}, true
}

// budgetParkedRunIDs lists the top-level runs still parked by the cap. Child
// runs are left out: resuming the parent continues them.
func (s *ClocheServer) budgetParkedRunIDs(ctx context.Context) []string {
	runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{State: domain.RunStateParked})
	if err != nil {
		return nil
	}
	var ids []string
	for _, r := range runs {
		if isBudgetParked(r) && r.ParentRunID == "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// ParkRunsForBudget parks every run still in flight (runs waiting on a poll
// step spend nothing and are left alone), across all projects, with
// the same state change `cloche loop stop --hard` makes (state "parked"), and
// stops the work behind them: the loop attempt's engine is cancelled and
// container runs are stopped. A parked run is resumed with `cloche resume`.
func (s *ClocheServer) ParkRunsForBudget(ctx context.Context) int {
	var parked int
	for _, state := range []domain.RunState{domain.RunStatePending, domain.RunStateRunning} {
		runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{State: state})
		if err != nil {
			log.Printf("budget: listing %s runs to park: %v", state, err)
			continue
		}
		for _, run := range runs {
			s.parkRunForBudget(ctx, run)
			parked++
		}
	}
	if parked > 0 {
		log.Printf("budget: parked %d run(s); resume them with `cloche resume`", parked)
	}
	return parked
}

func (s *ClocheServer) parkRunForBudget(ctx context.Context, run *domain.Run) {
	step := ""
	if len(run.ActiveSteps) > 0 {
		step = run.ActiveSteps[0]
	}
	if step != "" {
		_ = s.store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, kvBudgetParkStep, step)
	}

	run.State = domain.RunStateParked
	run.ParkedTitle = budgetParkTitle
	if err := s.store.UpdateRun(ctx, run); err != nil {
		log.Printf("budget: parking run %s: %v", run.ID, err)
		return
	}

	s.mu.Lock()
	if run.AttemptID != "" {
		s.budgetParked[run.AttemptID] = true
	}
	containerID, hasContainer := s.runIDs[run.ID]
	cancelFn, isHostRun := s.hostCancels[run.ID]
	attemptCancel, isLoopRun := s.attemptCancels[run.AttemptID]
	s.mu.Unlock()

	if hasContainer && s.container != nil {
		if err := s.container.Stop(ctx, containerID); err != nil {
			log.Printf("budget: stopping container for run %s: %v", run.ID, err)
		}
	}
	if isHostRun {
		cancelFn()
	}
	if isLoopRun {
		attemptCancel()
	}
	s.TriggerAttentionRefresh(run.ProjectDir)
}

// wasBudgetParked reports (and clears) whether the attempt was parked by the
// budget cap, so its run result is treated as a deliberate park rather than a
// failure.
func (s *ClocheServer) wasBudgetParked(attemptID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budgetParked[attemptID] {
		delete(s.budgetParked, attemptID)
		return true
	}
	return false
}

// budgetResumeStep returns the step a budget-parked run should resume from.
func (s *ClocheServer) budgetResumeStep(ctx context.Context, run *domain.Run) string {
	step, _, _ := s.store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, kvBudgetParkStep)
	return step
}

// isBudgetParked reports whether the run was parked by the budget cap.
func isBudgetParked(run *domain.Run) bool {
	return run.State == domain.RunStateParked && run.ParkedTitle == budgetParkTitle && run.ParkedThreadID == ""
}
