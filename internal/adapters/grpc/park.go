package grpc

import (
	"context"
	"log"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/ports"
)

// KV keys under which handleStepParked (executor.go) records what a resume
// needs to restart the exact container workflow/step that was parked.
const (
	kvParkImage       = "_help.park_image"
	kvParkContainerID = "_help.park_container_id"
	kvParkInnerStep   = "_help.park_inner_step"
	kvPendingAnswer   = "_help.pending_answer"
	kvPendingThreadID = "_help.pending_thread_id"
)

// ParkRunForHelp is the help.ParkFunc callback wired into the HelpRouter. It
// marks runID parked (with the thread it's awaiting a reply on) and sends
// ParkStep to the run's container so cloche-agent tears down the in-progress
// step. Best-effort: errors are logged, since the caller (Router.Ask, about
// to return {parked: true} to an agent/script that's being torn down
// regardless) has no way to act on them.
func (s *ClocheServer) ParkRunForHelp(ctx context.Context, runID, threadID, title string) {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		log.Printf("park: run %s not found: %v", runID, err)
		return
	}

	run.State = domain.RunStateParked
	run.ParkedThreadID = threadID
	run.ParkedTitle = title
	if err := s.store.UpdateRun(ctx, run); err != nil {
		log.Printf("park: failed to mark run %s parked: %v", runID, err)
	}

	s.publishHelpEvent(run.ProjectDir, runID, run.TaskID, run.AttemptID, "",
		activitylog.KindHelpParked, title, "help_parked: "+title)

	s.mu.Lock()
	containerID := s.runIDs[runID]
	s.mu.Unlock()
	if containerID == "" || s.pool == nil {
		log.Printf("park: no container known for run %s; container will not be torn down", runID)
		return
	}

	poolKey := s.pool.AttemptKeyForContainer(containerID)
	session := s.pool.GetSession(poolKey)
	if session == nil {
		log.Printf("park: no container session for run %s (containerID=%s)", runID, containerID)
		return
	}

	stepName := ""
	if n := len(run.ActiveSteps); n > 0 {
		stepName = run.ActiveSteps[n-1]
	}
	if err := session.ParkStep(stepName); err != nil {
		log.Printf("park: failed to send ParkStep for run %s: %v", runID, err)
	}
}

// resumeParkedRun restarts a parked run's container from its parked image
// and re-dispatches the parked step (resume=true), continuing the host
// workflow from there. body is the reply text that unblocked the run;
// threadID identifies the thread it came in on. Runs asynchronously to
// completion — the caller (ReplyThread) only waits for this to be launched.
func (s *ClocheServer) resumeParkedRun(ctx context.Context, run *domain.Run, threadID, body string) {
	resumeFrom := run.FindParkedStep()
	if resumeFrom == "" {
		log.Printf("resume: run %s is parked but has no step recorded with result %q", run.ID, domain.StepParked)
		return
	}

	parkImage, _, _ := s.store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, kvParkImage)
	parkContainerID, _, _ := s.store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, kvParkContainerID)
	if parkImage == "" {
		log.Printf("resume: run %s has no parked image recorded; cannot resume the container", run.ID)
		return
	}

	// Make the reply available to the in-container prompt adapter before
	// the step re-dispatches (see internal/adapters/agents/prompt).
	_ = s.store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, kvPendingAnswer, body)
	_ = s.store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, kvPendingThreadID, threadID)

	s.publishHelpEvent(run.ProjectDir, run.ID, run.TaskID, run.AttemptID, resumeFrom,
		activitylog.KindHelpResumed, body, "help_resumed: "+body)

	runner := &host.Runner{
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		ActivityLog:  s.activityLoggerFor(run.ProjectDir),
		Executor:     s.daemonExecutorForResume(run.ProjectDir, run.TaskID, run.AttemptID, parkContainerID, parkImage),
		TaskID:       run.TaskID,
		AttemptID:    run.AttemptID,
	}

	go func() {
		bgCtx := context.Background()
		result, runErr := runner.ResumeRun(bgCtx, run, resumeFrom)
		if runErr != nil {
			log.Printf("resume: run %s: %v", run.ID, runErr)
		}
		s.completeAttemptFromResult(run.AttemptID, run.TaskID, result, runErr)
	}()
}

// daemonExecutorForResume is like daemonExecutorFor but configures the
// executor to resume a parked container workflow: the sub-workflow whose
// ContainerID() matches parkedContainerID starts from parkedImage instead of
// a fresh container, and resume=true is set on every ExecuteStep so the
// in-container agent (e.g. the prompt adapter) continues its prior session.
func (s *ClocheServer) daemonExecutorForResume(projectDir, taskID, attemptID, parkedContainerID, parkedImage string) *DaemonExecutor {
	allWFs, _ := host.FindAllWorkflows(projectDir)
	image := s.defaultImage
	if projCfg, err := config.Load(projectDir); err == nil && projCfg.Daemon.Image != "" {
		image = projCfg.Daemon.Image
	}
	var de *DaemonExecutor
	de = NewDaemonExecutor(DaemonExecutorConfig{
		Pool:              s.pool,
		Store:             s.store,
		LogStore:          s.logStore,
		LogBroadcast:      s.logBroadcast,
		ProjectDir:        projectDir,
		ContainerSeedSHA:  gitHEAD(projectDir),
		TaskID:            taskID,
		AttemptID:         attemptID,
		Image:             image,
		AllWFs:            allWFs,
		ResumeMode:        true,
		ParkedContainerID: parkedContainerID,
		ParkedImage:       parkedImage,
		OnContainerStart: func(containerID string) {
			runID := ""
			if de.hostExec != nil {
				runID = de.hostExec.HostRunID
			}
			if runID != "" {
				s.mu.Lock()
				s.containerRun[containerID] = runID
				s.runIDs[runID] = containerID
				s.mu.Unlock()
			}
		},
	})
	return de
}

// triggerResumeIfParked checks whether the given thread's run is parked and,
// if so, launches resumeParkedRun. Called after a reply is successfully
// appended to a thread. Best-effort: logs and returns on lookup failure
// rather than failing the ReplyThread RPC, since the reply itself already
// succeeded.
func (s *ClocheServer) triggerResumeIfParked(ctx context.Context, threadID, body string) {
	hs, ok := s.store.(ports.HelpStore)
	if !ok {
		return
	}
	thread, _, err := hs.GetThread(ctx, threadID)
	if err != nil || thread == nil || thread.RunID == "" {
		return
	}
	run, err := s.store.GetRun(ctx, thread.RunID)
	if err != nil {
		return
	}
	if run.State != domain.RunStateParked {
		return
	}
	s.resumeParkedRun(context.Background(), run, thread.ID, body)
}
