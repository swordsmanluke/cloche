package grpc

import (
	"context"
	"log"
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
	return s.budget != nil && s.budget.Blocked(context.Background())
}

// GetBudget implements the GetBudget RPC.
func (s *ClocheServer) GetBudget(ctx context.Context, _ *pb.GetBudgetRequest) (*pb.GetBudgetResponse, error) {
	if s.budget == nil {
		return &pb.GetBudgetResponse{}, nil
	}
	s.budget.Blocked(ctx) // refresh a stale reading
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

// budgetAttentionItem returns the daemon-wide "Needs you" item for an
// exceeded cap, naming the cap and when the day's window clears.
func (s *ClocheServer) budgetAttentionItem() (attention.Item, bool) {
	if s.budget == nil {
		return attention.Item{}, false
	}
	st := s.budget.Status()
	if !st.Enabled || !st.Over {
		return attention.Item{}, false
	}
	reason := budgetReason(st)
	return attention.Item{
		Kind:   attention.KindBudget,
		Reason: reason,
		Since:  st.OverSince,
		Key:    "budget",
	}, true
}

func budgetReason(st budget.Status) string {
	return st.Describe(time.Now())
}

// ParkRunsForBudget parks every run still in flight, across all projects, with
// the same state change `cloche loop stop --hard` makes (state "parked"), and
// stops the work behind them: the loop attempt's engine is cancelled and
// container runs are stopped. A parked run is resumed with `cloche resume`.
func (s *ClocheServer) ParkRunsForBudget(ctx context.Context) int {
	var parked int
	for _, state := range []domain.RunState{domain.RunStatePending, domain.RunStateRunning, domain.RunStateWaiting} {
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
