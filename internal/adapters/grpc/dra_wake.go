package grpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/host"
)

// DRA wake-ups (autonomous-loop design, Part 5 "How it lives"). A wake-up is
// a recorded host run, "dra-wake-<name>", built in Go from dra.WakeSpec: one
// container prompt step in the DRA image, resuming the DRA's named Claude
// Code session or, if there is none or resuming fails, starting a new one.

// ErrWakeInProgress is returned when a DRA already has a wake-up running.
// Wake-ups are refused, not queued: a DRA that is already awake will see the
// new state of the world (and the tracker) when it next looks, and a queue
// would only replay stale messages after it.
var ErrWakeInProgress = errors.New("a wake-up of this DRA is already running")

// ErrBudgetBlocked is returned for an automatic wake-up while the daily
// budget cap is exceeded. A wake-up started by hand is only warned about.
var ErrBudgetBlocked = errors.New("daily budget cap reached: no new DRA wake-ups launch")

func wakeKey(projectDir, name string) string { return projectDir + "\x00" + name }

// WakeDRA starts a wake-up of the named DRA with a message saying what
// happened, and returns its run id. The run proceeds in the background.
// manual is true for `cloche dra wake`, which the budget cap never blocks;
// every automatic trigger passes false and is held back while the cap is
// exceeded.
func (s *ClocheServer) WakeDRA(ctx context.Context, projectDir, name, message string, manual bool) (string, error) {
	projectDir = normalizeProjectDir(projectDir)
	d, err := dra.Find(projectDir, name)
	if err != nil {
		return "", err
	}
	if message == "" {
		return "", fmt.Errorf("a wake-up needs a message saying what happened")
	}
	if !manual && s.budgetBlocked() {
		return "", ErrBudgetBlocked
	}
	if s.container == nil {
		return "", fmt.Errorf("no container runtime configured")
	}
	if err := dra.EnsureNotes(d); err != nil {
		return "", fmt.Errorf("creating notes.md: %w", err)
	}

	// One wake-up per DRA at a time.
	key := wakeKey(projectDir, name)
	s.mu.Lock()
	if running, busy := s.wakeActive[key]; busy {
		s.mu.Unlock()
		return "", fmt.Errorf("%w: DRA %q, run %s", ErrWakeInProgress, name, running)
	}
	if s.wakeActive == nil {
		s.wakeActive = map[string]string{}
	}
	s.wakeActive[key] = "starting"
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.wakeActive, key)
		s.mu.Unlock()
	}

	sessions, err := dra.LoadSessions(projectDir)
	if err != nil {
		release()
		return "", err
	}
	attemptID := s.ensureTaskAndAttempt(ctx, "", "DRA wake-up: "+name, projectDir, nil)
	taskID := "user-" + attemptID
	runID := domain.GenerateRunID(dra.WorkflowName(name), attemptID)
	spec := dra.WakeSpec{Name: name, RunID: runID, ResumeID: sessions[name].ID, NewID: dra.NewSessionID()}
	if err := dra.WriteMessage(projectDir, runID, message); err != nil {
		release()
		return "", fmt.Errorf("writing wake-up message: %w", err)
	}
	hostWF, containers := spec.Workflows()

	exec, ok := s.daemonExecutorFor(projectDir, taskID, attemptID).(*DaemonExecutor)
	if !ok {
		release()
		return "", fmt.Errorf("daemon executor unavailable")
	}
	if exec.allWFs == nil {
		exec.allWFs = map[string]*domain.Workflow{}
	}
	for n, wf := range containers {
		exec.allWFs[n] = wf
	}
	// notes.md is usually untracked, so it is missing from a snapshot of
	// HEAD. Seed the container from the live tree, which has it; the DRA's
	// project is read-only anyway, so there is nothing to protect.
	exec.containerSeed = RepoSeed{}
	exec.containerSeedChildren = nil

	s.mu.Lock()
	s.wakeActive[key] = runID
	s.mu.Unlock()
	_ = dra.UpdateSession(projectDir, name, func(ss *dra.Session) {
		ss.LastRunID, ss.LastWakeAt, ss.LastResult, ss.LastMode = runID, time.Now(), "running", ""
	})

	runner := &host.Runner{
		Store:         s.store,
		Captures:      s.captures,
		LogBroadcast:  s.logBroadcast,
		ActivityLog:   s.activityLoggerFor(projectDir),
		Executor:      exec,
		TaskID:        taskID,
		AttemptID:     attemptID,
		UserInitiated: manual,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.hostCancels[runID] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.hostCancels, runID)
			s.mu.Unlock()
			cancel()
			release()
		}()
		result, runErr := runner.RunWorkflowDef(runCtx, projectDir, hostWF, runID)
		state := domain.RunStateFailed
		if result != nil {
			state = result.State
		}
		if runErr != nil {
			log.Printf("dra wake-up %s (%s): %v", runID, name, runErr)
		}
		var steps []*domain.StepExecution
		if run, err := s.store.GetRun(context.Background(), runID); err == nil && run != nil {
			steps = run.StepExecutions
		}
		id, mode := spec.Outcome(steps)
		if err := dra.UpdateSession(projectDir, name, func(ss *dra.Session) {
			ss.ID, ss.LastResult, ss.LastMode = id, string(state), mode
		}); err != nil {
			log.Printf("dra wake-up %s: saving session: %v", runID, err)
		}
		s.completeAttemptRecord(attemptID, state)
		s.TriggerAttentionRefresh(projectDir)
	}()
	return runID, nil
}

// runDRAWake handles a dra.WakeRequest arriving over RunWorkflow (`cloche dra
// wake`): always a hand-started wake-up.
func (s *ClocheServer) runDRAWake(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	runID, err := s.WakeDRA(ctx, req.ProjectDir, req.Env[dra.WakeEnvDRA], req.Env[dra.WakeEnvMessage], true)
	if err != nil {
		return nil, err
	}
	return &pb.RunWorkflowResponse{RunId: runID}, nil
}
