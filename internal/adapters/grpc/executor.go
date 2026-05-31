package grpc

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
)

// DaemonExecutor implements engine.StepExecutor and routes steps based on the
// workflow's location. Host steps are delegated to the embedded host.Executor.
// Container steps are dispatched to the in-container agent via the ContainerPool.
// Workflow_name steps look up the target workflow and run it recursively using
// the appropriate executor.
type DaemonExecutor struct {
	// hostExec handles script and agent steps in host workflows.
	hostExec *host.Executor

	// pool manages container sessions for container workflow steps.
	pool *docker.ContainerPool

	// projectDir is the project root directory, used to look up workflows and
	// build container configs.
	projectDir string

	// attemptID identifies the current attempt, used as the pool session key.
	attemptID string

	// image is the container image to use when starting new containers.
	image string

	// allWFs is the full set of workflows (host and container) for the project,
	// keyed by name. Used to resolve workflow_name step targets.
	allWFs map[string]*domain.Workflow

	// store is used to set child_run_id in the KV store after extracting
	// container results to a git branch.
	store ports.RunStore

	// logStore is used to index extracted container step log files so the web
	// UI can serve them by step name. Optional: indexing is skipped when nil.
	logStore ports.LogStore

	// logBroadcast is used to publish live log lines for nested sub-workflow
	// steps so that cloche logs -f and the web UI show them in real time.
	// Optional: broadcasting is skipped when nil.
	logBroadcast *logstream.Broadcaster

	// taskID is the task ID for KV store operations.
	taskID string

	// resumeMode, when true, sets the resume flag on all ExecuteStep messages
	// so the in-container agent continues an existing LLM conversation.
	resumeMode bool

	// onContainerStart is called after a container is started with (containerID).
	// The server uses this to register the container → run mapping so the
	// AgentSession handler can route StepLog messages to the right run.
	onContainerStart func(containerID string)

	// poolKeys tracks container pool keys used by this executor so Close()
	// can clean them all up after the host workflow finishes.
	poolKeys map[string]bool

	// worktrees tracks pre-created extraction worktrees keyed by pool key. One
	// slice per container (pool key) — shared across sub-workflows that reuse
	// the same container.id within an attempt. Each slice has one entry per
	// repo the workflow extracts into (legacy single-tree projects produce a
	// single-element slice with an unnamed repo).
	worktrees map[string][]repoWorktree

	// closed tracks whether Close() has already been called.
	closed bool
}

// DaemonExecutorConfig holds configuration for constructing a DaemonExecutor.
type DaemonExecutorConfig struct {
	HostExec   *host.Executor
	Pool       *docker.ContainerPool
	Store      ports.RunStore
	LogStore   ports.LogStore
	LogBroadcast *logstream.Broadcaster
	ProjectDir string
	TaskID     string
	AttemptID  string
	Image      string
	AllWFs     map[string]*domain.Workflow
	// ResumeMode, when true, sets resume=true on all ExecuteStep messages so
	// that the in-container agent continues its previous LLM conversation.
	ResumeMode bool
	// OnContainerStart is called after a container starts with (containerID).
	OnContainerStart func(containerID string)
}

// NewDaemonExecutor creates a DaemonExecutor from the given config.
func NewDaemonExecutor(cfg DaemonExecutorConfig) *DaemonExecutor {
	return &DaemonExecutor{
		hostExec:         cfg.HostExec,
		pool:             cfg.Pool,
		store:            cfg.Store,
		logStore:         cfg.LogStore,
		logBroadcast:     cfg.LogBroadcast,
		projectDir:       cfg.ProjectDir,
		taskID:           cfg.TaskID,
		attemptID:        cfg.AttemptID,
		image:            cfg.Image,
		allWFs:           cfg.AllWFs,
		resumeMode:       cfg.ResumeMode,
		onContainerStart: cfg.OnContainerStart,
		worktrees:        make(map[string][]repoWorktree),
	}
}

// Ensure DaemonExecutor satisfies engine.StepExecutor.
var _ engine.StepExecutor = (*DaemonExecutor)(nil)

// Package-level hooks so tests can stub out the docker extract functions
// without standing up real containers or git operations.
var (
	prepareExtractWorktreeFn = docker.PrepareExtractWorktree
	extractResultsFn         = docker.ExtractResults
)

// Close cleans up all container pool entries used by this executor. Successful
// containers are stopped and removed; failed containers are stopped but kept
// for debugging. Pre-created extraction worktrees follow the same policy:
// removed on success, kept on failure. Must be called after the host workflow
// finishes.
func (d *DaemonExecutor) Close(succeeded bool) {
	if d.closed || d.pool == nil {
		return
	}
	d.closed = true
	ctx := context.Background()
	for key := range d.poolKeys {
		if err := d.pool.CleanupAttempt(ctx, key, false, succeeded); err != nil {
			log.Printf("daemon executor: cleanup pool key %s: %v", key, err)
		}
	}
	if succeeded {
		for key, repos := range d.worktrees {
			for _, p := range repos {
				removeExtractWorktree(ctx, p.Repo.Path, p.Worktree)
			}
			delete(d.worktrees, key)
		}
	}
}

// repoWorktree pairs a resolved repo with the extract worktree+branch the
// daemon prepared in that repo's git dir.
type repoWorktree struct {
	Repo     resolvedRepo
	Worktree docker.ExtractWorktree
}

// removeExtractWorktree removes a pre-created extraction worktree and its
// branch. The git commands run with cwd = repoDir, which must be the repo
// whose git tracks the branch/worktree (the wrapper-project root for legacy
// single-tree mode, or a per-repo path for multi-repo). Best-effort: errors
// are logged but not returned.
func removeExtractWorktree(ctx context.Context, repoDir string, wt docker.ExtractWorktree) {
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wt.Dir)
	rmCmd.Dir = repoDir
	if out, err := rmCmd.CombinedOutput(); err != nil {
		log.Printf("daemon executor: git worktree remove %s: %s: %v", wt.Dir, out, err)
	}
	if wt.Branch != "" {
		brCmd := exec.CommandContext(ctx, "git", "branch", "-D", wt.Branch)
		brCmd.Dir = repoDir
		if out, err := brCmd.CombinedOutput(); err != nil {
			log.Printf("daemon executor: git branch -D %s: %s: %v", wt.Branch, out, err)
		}
	}
}

// SetHostExecutor replaces the host executor with a fully-configured one.
// Implements engine.HostExecutorConfigurer.
func (d *DaemonExecutor) SetHostExecutor(exec engine.StepExecutor) {
	if he, ok := exec.(*host.Executor); ok {
		d.hostExec = he
	}
}

// Execute routes the step to the appropriate executor based on workflow location.
func (d *DaemonExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	// workflow_name steps are handled at the daemon level regardless of which
	// workflow they appear in.
	if step.Type == domain.StepTypeWorkflow {
		return d.executeWorkflowStep(ctx, step)
	}

	wf, ok := engine.WorkflowFromContext(ctx)
	if !ok {
		return domain.StepResult{}, fmt.Errorf("daemon executor: no workflow in context for step %q", step.Name)
	}

	if wf.Location == domain.LocationHost {
		return d.hostExec.Execute(ctx, step)
	}

	return d.executeContainerStep(ctx, step, wf)
}

// executeWorkflowStep looks up the target workflow by name, then runs it as a
// sub-workflow using this same executor, mapping the final state to a step result.
// For container sub-workflows, it pre-creates a shared extraction worktree and
// branch on first encounter of a pool key (so multiple sub-workflows that reuse
// the same container.id share one worktree), extracts the container workspace
// into it after the sub-workflow completes, and sets child_run_id / child_branch
// in the KV store so downstream merge steps can find the result.
func (d *DaemonExecutor) executeWorkflowStep(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	targetName := step.Config["workflow_name"]
	if targetName == "" {
		return domain.StepResult{}, fmt.Errorf("workflow step %q missing workflow_name config", step.Name)
	}

	targetWF, ok := d.allWFs[targetName]
	if !ok {
		return domain.StepResult{}, fmt.Errorf("workflow step %q: workflow %q not found in project", step.Name, targetName)
	}

	// Generate a run ID for the child workflow.
	childRunID := domain.GenerateRunID(targetName, d.attemptID)

	log.Printf("daemon executor: running sub-workflow %q for step %q (childRunID=%s)", targetName, step.Name, childRunID)

	// For container sub-workflows, register a deferred cleanup on failure so
	// the container is stopped if eng.Run returns an error (e.g. context
	// cancellation, daemon restart). On success the container stays in the pool
	// so subsequent sub-workflows sharing the same container.id can reuse it.
	// Final cleanup of successful containers happens in Close().
	succeeded := false
	var poolKey string
	if targetWF.Location == domain.LocationContainer && d.pool != nil {
		poolKey = d.attemptID + ":" + targetWF.ContainerID()
		if d.poolKeys == nil {
			d.poolKeys = make(map[string]bool)
		}
		d.poolKeys[poolKey] = true
		defer func() {
			if !succeeded {
				// Use background context: the original ctx may already be cancelled.
				cleanupCtx := context.Background()
				_ = d.pool.CleanupAttempt(cleanupCtx, poolKey, false, false)
			}
		}()

		// Pre-create the extraction worktree+branch for this pool key on the
		// first sub-workflow that uses it. Subsequent sub-workflows reusing
		// the same container share these same worktrees — each extraction adds
		// a commit to the shared branch (one per repo).
		if _, exists := d.worktrees[poolKey]; !exists {
			if err := d.prepareExtractWorktrees(ctx, poolKey, targetWF); err != nil {
				log.Printf("daemon executor: prepare extract worktrees: %v", err)
			}
		}
	}

	eng := engine.New(d)
	// For host sub-workflows attach a lightweight status handler so the inner
	// steps' events (start, output, completion) are broadcast live to the
	// parent run's log stream and can be read back from full.log.
	if targetWF.Location == domain.LocationHost && d.logBroadcast != nil && d.hostExec != nil && d.hostExec.HostRunID != "" {
		eng.SetStatusHandler(&innerHostStatusHandler{
			logBroadcast: d.logBroadcast,
			hostRunID:    d.hostExec.HostRunID,
			outputDir:    filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID),
		})
	}
	run, err := eng.Run(ctx, targetWF)
	if err != nil {
		log.Printf("daemon executor: sub-workflow %q failed: %v", targetName, err)
		// Even on error (e.g. context timeout), try to extract container logs so
		// they are accessible for post-mortem investigation. The original ctx may
		// already be cancelled, so use a background context. Only attempt
		// extraction if a session actually exists (i.e. the container started
		// before the failure).
		if targetWF.Location == domain.LocationContainer && d.pool != nil {
			if session := d.pool.GetSession(poolKey); session != nil {
				bgCtx := context.Background()
				d.extractContainerLogs(bgCtx, session.ContainerID, step.Name)
				if d.logStore != nil && d.hostExec != nil && d.hostExec.HostRunID != "" {
					subDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID, step.Name)
					d.indexSubworkflowLogs(bgCtx, d.hostExec.HostRunID, subDir)
				}
			}
		}
		return domain.StepResult{Result: "fail"}, nil
	}

	succeeded = run.State == domain.RunStateSucceeded
	resultLabel := "failed"
	if succeeded {
		resultLabel = "succeeded"
	}

	// For host sub-workflows, aggregate inner step logs into <step.Name>.log so
	// the outer hostStatusHandler.OnStepComplete can read them and append them
	// to the parent run's full.log.
	if targetWF.Location == domain.LocationHost {
		d.aggregateHostSubWorkflowLogs(step.Name, run)
	}

	// For container sub-workflows, extract the workspace into the pre-created
	// worktree and copy logs to the host log directory.
	if targetWF.Location == domain.LocationContainer {
		// Get the session so we can access the container for extraction.
		session, sessErr := d.pool.SessionFor(ctx, poolKey, ports.ContainerConfig{})
		if sessErr != nil {
			log.Printf("daemon executor: could not get session for extraction: %v", sessErr)
		}

		prepared, hasWorktrees := d.worktrees[poolKey]
		if hasWorktrees && session != nil {
			authorName, authorEmail := resolveGitIdentity(d.projectDir)
			for _, p := range prepared {
				log.Printf("daemon executor: extracting results for repo %q to branch %s", p.Repo.Name, p.Worktree.Branch)
				if _, err := extractResultsFn(ctx, docker.ExtractOptions{
					ContainerID:      session.ContainerID,
					WorktreeDir:      p.Worktree.Dir,
					Branch:           p.Worktree.Branch,
					BaseSHA:          d.resolveRepoBaseSHA(ctx, p.Repo.Path),
					RunID:            childRunID,
					WorkflowName:     targetName,
					Result:           resultLabel,
					ContainerSubPath: p.Repo.SubPath,
					AuthorName:       authorName,
					AuthorEmail:      authorEmail,
				}); err != nil {
					log.Printf("daemon executor: failed to extract results for repo %q: %v", p.Repo.Name, err)
				} else {
					log.Printf("daemon executor: branch %s in %s updated", p.Worktree.Branch, p.Repo.Path)
				}
			}
		}

		// Extract container output logs to the host log directory so that
		// the host status handler can read them (it looks for <step>.log)
		// and they survive container cleanup.
		if session != nil {
			d.extractContainerLogs(ctx, session.ContainerID, step.Name)
			if d.logStore != nil && d.hostExec != nil && d.hostExec.HostRunID != "" {
				subDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID, step.Name)
				d.indexSubworkflowLogs(ctx, d.hostExec.HostRunID, subDir)
			}
		}

		// Set child_run_id (latest child) so existing host steps still have
		// a run handle to work with.
		if d.store != nil && d.taskID != "" {
			var kvRunID string
			if d.hostExec != nil {
				kvRunID = d.hostExec.HostRunID
			}
			_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, kvRunID, "child_run_id", childRunID)
		}

		// Note: CleanupAttempt is called by the deferred function above.
	}

	if succeeded {
		return domain.StepResult{Result: "success"}, nil
	}
	return domain.StepResult{Result: "fail"}, nil
}

// resolveRepoBaseSHA picks the git SHA the extract worktree for a particular
// repo should be branched from. Sub-workflows often pick their own base at
// runtime (e.g. vertical's per-layer base = the previously-merged layer's
// branch), so the host's HEAD is the wrong default — we use it only as a last
// resort.
//
// Lookup order:
//  1. current_base_branch KV at the host run's scope (set by host steps that
//     pick a branch — e.g. vertical-pick-layer.sh), resolved against repoDir.
//  2. repoDir's HEAD.
//
// Branch names are resolved against either the local branch or its origin
// remote-tracking ref (since freshly pushed branches may not have a local
// tracking branch on the host).
func (d *DaemonExecutor) resolveRepoBaseSHA(ctx context.Context, repoDir string) string {
	if d.store != nil && d.taskID != "" {
		var hostRunID string
		if d.hostExec != nil {
			hostRunID = d.hostExec.HostRunID
		}
		val, found, _ := d.store.GetContextKey(ctx, d.taskID, d.attemptID, hostRunID, "current_base_branch")
		if found && val != "" {
			if sha := gitResolveRef(repoDir, val); sha != "" {
				return sha
			}
			log.Printf("daemon executor: current_base_branch=%q could not be resolved in %s; falling back to HEAD", val, repoDir)
		}
	}
	return gitHEAD(repoDir)
}

// prepareExtractWorktrees pre-creates one extraction worktree+branch per repo
// the workflow extracts into, recording them on the executor. Called once per
// pool key, on the first sub-workflow that uses the container. Writes
// child_branch:<repo> and child_repo_path:<repo> to the KV store so host-
// workflow scripts can iterate the branches. Also writes a comma-separated
// child_repos list and, for compatibility with single-repo / legacy scripts,
// the bare child_branch key when exactly one worktree was prepared.
func (d *DaemonExecutor) prepareExtractWorktrees(ctx context.Context, poolKey string, wf *domain.Workflow) error {
	cfg, err := config.Load(d.projectDir)
	if err != nil {
		log.Printf("daemon executor: config.Load(%s): %v — treating as no [[repositories]] declared", d.projectDir, err)
		cfg = nil
	}
	repos, err := resolveRepos(wf, cfg, d.projectDir)
	if err != nil {
		return err
	}

	branchSuffix := d.attemptID + "-" + wf.ContainerID()
	branchName := "cloche/" + branchSuffix

	var prepared []repoWorktree
	for _, r := range repos {
		baseSHA := d.resolveRepoBaseSHA(ctx, r.Path)
		if baseSHA == "" {
			rollbackWorktrees(ctx, prepared)
			return fmt.Errorf("could not resolve base SHA for repo %q at %s", r.Name, r.Path)
		}
		wt, prepErr := prepareExtractWorktreeFn(ctx, docker.PrepareOptions{
			ProjectDir: r.Path,
			BaseSHA:    baseSHA,
			TargetDir:  filepath.Join(r.Path, ".gitworktrees", "cloche", branchSuffix),
			Branch:     branchName,
		})
		if prepErr != nil {
			rollbackWorktrees(ctx, prepared)
			return fmt.Errorf("preparing worktree for repo %q: %w", r.Name, prepErr)
		}
		log.Printf("daemon executor: prepared extract worktree at %s on branch %s (repo=%q)", wt.Dir, wt.Branch, r.Name)
		prepared = append(prepared, repoWorktree{Repo: r, Worktree: wt})
	}
	d.worktrees[poolKey] = prepared
	d.writeRepoBranchKV(ctx, prepared)
	return nil
}

// rollbackWorktrees removes any worktrees that were prepared as part of a
// multi-repo prepare batch that subsequently failed. Best-effort.
// Calls removeExtractWorktree for each entry; errors are logged, not returned.
func rollbackWorktrees(ctx context.Context, prepared []repoWorktree) {
	for _, p := range prepared {
		removeExtractWorktree(ctx, p.Repo.Path, p.Worktree)
	}
}

// writeRepoBranchKV records the prepared extract branches in the KV store so
// host-workflow scripts can locate them. For each named repo, writes both
// child_branch:<name> (the branch) and child_repo_path:<name> (the absolute
// repo path). Writes child_repos as a comma-separated list of names for
// enumeration. When exactly one worktree was prepared (single-repo or legacy),
// also writes the bare child_branch key so scripts that haven't been migrated
// to per-repo keys continue to work.
func (d *DaemonExecutor) writeRepoBranchKV(ctx context.Context, prepared []repoWorktree) {
	if d.store == nil || d.taskID == "" {
		return
	}
	var kvRunID string
	if d.hostExec != nil {
		kvRunID = d.hostExec.HostRunID
	}
	var names []string
	for _, p := range prepared {
		if p.Repo.Name != "" {
			names = append(names, p.Repo.Name)
			_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, kvRunID, "child_branch:"+p.Repo.Name, p.Worktree.Branch)
			_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, kvRunID, "child_repo_path:"+p.Repo.Name, p.Repo.Path)
		}
	}
	if len(names) > 0 {
		_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, kvRunID, "child_repos", strings.Join(names, ","))
	}
	if len(prepared) == 1 {
		_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, kvRunID, "child_branch", prepared[0].Worktree.Branch)
	}
}


// executeContainerStep obtains a container session for the attempt (starting a
// new container if needed) and dispatches the step to the in-container agent.
func (d *DaemonExecutor) executeContainerStep(ctx context.Context, step *domain.Step, wf *domain.Workflow) (domain.StepResult, error) {
	if d.pool == nil {
		return domain.StepResult{}, fmt.Errorf("daemon executor: no container pool configured")
	}
	if d.attemptID == "" {
		return domain.StepResult{}, fmt.Errorf("daemon executor: attemptID not set for container step %q", step.Name)
	}

	// Use the workflow's container ID as part of the pool key so that workflows
	// sharing the same container ID reuse the same session within an attempt.
	poolKey := d.attemptID + ":" + wf.ContainerID()

	var hostRunID string
	if d.hostExec != nil {
		hostRunID = d.hostExec.HostRunID
	}

	image := d.image
	if wfImage, ok := wf.Config["container.image"]; ok && wfImage != "" {
		image = wfImage
	}

	cfg := ports.ContainerConfig{
		Image:        image,
		WorkflowName: wf.Name,
		ProjectDir:   d.projectDir,
		RunID:        hostRunID,
		TaskID:       d.taskID,
		AttemptID:    d.attemptID,
		NetworkAllow: []string{"*"},
		// Start agent in session mode (no workflow file argument) so it
		// connects to the daemon via gRPC and waits for ExecuteStep commands.
		Cmd: []string{"cloche-agent"},
	}

	// Inject workflow-level `container { agent_command = ... }` and
	// `container { agent_args = ... }` into the step config so the in-container
	// cloche-agent's prompt adapter picks them up. Without this the adapter
	// falls back to its built-in default of ["claude"] regardless of what the
	// .cloche file declares. Step-level overrides win.
	if step.Config == nil {
		step.Config = map[string]string{}
	}
	if _, has := step.Config["agent_command"]; !has {
		if cmd := wf.Config["container.agent_command"]; cmd != "" {
			step.Config["agent_command"] = cmd
		}
	}
	if _, has := step.Config["agent_args"]; !has {
		if args := wf.Config["container.agent_args"]; args != "" {
			step.Config["agent_args"] = args
		}
	}

	// Set agent credential dirs so the container runtime can copy auth files
	// without knowing which agent is in use. Defaults to Claude Code's paths;
	// other agents override via container.agent_command in the workflow.
	agentCmd := step.Config["agent_command"]
	if agentCmd == "" {
		agentCmd = "claude"
	}
	if agentCmd == "claude" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			cfg.AgentCredsHostDir = filepath.Join(home, ".claude")
			cfg.AgentCredsContainerDir = "/home/agent/.claude/"
		}
	}

	session, err := d.pool.SessionFor(ctx, poolKey, cfg)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("daemon executor: getting container session for step %q: %w", step.Name, err)
	}

	if d.onContainerStart != nil {
		d.onContainerStart(session.ContainerID)
	}

	return session.ExecuteStep(ctx, step, d.resumeMode)
}

// extractContainerLogs copies output log files from the container to the host
// log directory. The container's full.log is written as <stepName>.log so the
// host status handler (which reads <outputDir>/<step>.log on step completion)
// can pick it up and append it to the host workflow's full.log. Individual
// container step logs are placed in a <stepName>/ subdirectory.
func (d *DaemonExecutor) extractContainerLogs(ctx context.Context, containerID, stepName string) {
	if d.pool == nil || d.taskID == "" || d.attemptID == "" {
		return
	}

	hostLogDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID)

	// Extract container output to a step-specific subdirectory so individual
	// container step logs (implement.log, test.log, etc.) are preserved
	// without colliding with the host workflow's own log files.
	subDir := filepath.Join(hostLogDir, stepName)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		log.Printf("daemon executor: failed to create log subdir %s: %v", subDir, err)
		return
	}

	if err := d.pool.CopyFrom(ctx, containerID, "/workspace/.cloche/output/.", subDir); err != nil {
		log.Printf("daemon executor: failed to extract container logs: %v", err)
		return
	}

	// Copy the container's full.log as <stepName>.log in the host log dir.
	// The host status handler reads this file on step completion and appends
	// its content to the host workflow's full.log.
	containerFullLog := filepath.Join(subDir, "full.log")
	data, err := os.ReadFile(containerFullLog)
	if err != nil {
		log.Printf("daemon executor: no full.log in container output: %v", err)
		return
	}
	stepLog := filepath.Join(hostLogDir, stepName+".log")
	if err := os.WriteFile(stepLog, data, 0644); err != nil {
		log.Printf("daemon executor: failed to write %s: %v", stepLog, err)
	}
}

// indexSubworkflowLogs scans the sub-workflow log directory (e.g. develop/) and
// registers each .log file in the log_files table under hostRunID so the web UI
// can serve individual container step logs (implement, compile, test, etc.).
func (d *DaemonExecutor) indexSubworkflowLogs(ctx context.Context, hostRunID, subDir string) {
	entries, err := os.ReadDir(subDir)
	if err != nil {
		log.Printf("daemon executor: failed to read subdir %s for log indexing: %v", subDir, err)
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}

		var fileType, stepName string
		base := strings.TrimSuffix(name, ".log")

		switch {
		case name == "full.log":
			fileType = "full"
		case name == "container.log":
			continue
		case strings.HasPrefix(name, "llm-"):
			fileType = "llm"
			stepName = strings.TrimPrefix(base, "llm-")
		default:
			fileType = "script"
			stepName = base
		}

		info, _ := entry.Info()
		var fileSize int64
		if info != nil {
			fileSize = info.Size()
		}

		logEntry := &ports.LogFileEntry{
			RunID:     hostRunID,
			StepName:  stepName,
			FileType:  fileType,
			FilePath:  filepath.Join(subDir, name),
			FileSize:  fileSize,
			CreatedAt: now,
		}
		if err := d.logStore.SaveLogFile(ctx, logEntry); err != nil {
			log.Printf("daemon executor: failed to index log file %s for run %s: %v", name, hostRunID, err)
		}
	}
}

// aggregateHostSubWorkflowLogs concatenates the log files written by a host
// sub-workflow's individual steps into a single file named <workflowStepName>.log
// in the shared output directory. This file is then picked up by the outer
// hostStatusHandler.OnStepComplete so the sub-workflow's content is included
// in the parent run's full.log.
func (d *DaemonExecutor) aggregateHostSubWorkflowLogs(workflowStepName string, run *domain.Run) {
	if d.taskID == "" || d.attemptID == "" || run == nil {
		return
	}
	outputDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID)
	var sb strings.Builder
	for _, exec := range run.StepExecutions {
		logPath := filepath.Join(outputDir, exec.StepName+".log")
		data, err := os.ReadFile(logPath)
		if err != nil || len(data) == 0 {
			continue
		}
		sb.Write(data)
	}
	if sb.Len() == 0 {
		return
	}
	aggregatedPath := filepath.Join(outputDir, workflowStepName+".log")
	if err := os.WriteFile(aggregatedPath, []byte(sb.String()), 0644); err != nil {
		log.Printf("daemon executor: failed to write aggregated log for %s: %v", workflowStepName, err)
	}
}

// innerHostStatusHandler is a lightweight engine.StatusHandler attached to the
// engine running a host sub-workflow. It broadcasts inner step events (start,
// output, completion) to the parent run's log broadcaster so that live-stream
// clients see the nested workflow activity in real time.
type innerHostStatusHandler struct {
	logBroadcast *logstream.Broadcaster
	hostRunID    string
	outputDir    string
}

func (h *innerHostStatusHandler) OnStepStart(_ *domain.Run, step *domain.Step) {
	if h.logBroadcast == nil {
		return
	}
	h.logBroadcast.Publish(h.hostRunID, logstream.LogLine{
		Timestamp: time.Now().Format(time.RFC3339),
		Type:      "status",
		Content:   "step_started: " + step.Name,
		StepName:  step.Name,
	})
}

func (h *innerHostStatusHandler) OnStepComplete(_ *domain.Run, step *domain.Step, result string, _ *domain.TokenUsage) {
	if h.logBroadcast == nil {
		return
	}
	logPath := filepath.Join(h.outputDir, step.Name+".log")
	if data, err := os.ReadFile(logPath); err == nil && len(data) > 0 {
		h.logBroadcast.Publish(h.hostRunID, logstream.LogLine{
			Timestamp: time.Now().Format(time.RFC3339),
			Type:      "script",
			Content:   string(data),
			StepName:  step.Name,
		})
	}
	h.logBroadcast.Publish(h.hostRunID, logstream.LogLine{
		Timestamp: time.Now().Format(time.RFC3339),
		Type:      "status",
		Content:   "step_completed: " + step.Name + " -> " + result,
		StepName:  step.Name,
	})
}

func (h *innerHostStatusHandler) OnStepSkipped(_ *domain.Run, step *domain.Step, wire string) {
	if h.logBroadcast == nil {
		return
	}
	h.logBroadcast.Publish(h.hostRunID, logstream.LogLine{
		Timestamp: time.Now().Format(time.RFC3339),
		Type:      "status",
		Content:   "step_skipped: " + step.Name + " -> " + wire,
		StepName:  step.Name,
	})
}

func (h *innerHostStatusHandler) OnRunComplete(_ *domain.Run) {}

// resolveGitIdentity loads the merged (global+project) git identity for
// extraction commits. Returns empty strings when no identity is configured,
// which lets ExtractResults fall back to its built-in "cloche <cloche@local>"
// default. Load errors are logged and treated as unset.
func resolveGitIdentity(projectDir string) (name, email string) {
	cfg, err := config.LoadMerged(projectDir)
	if err != nil {
		log.Printf("daemon executor: loading git identity from config: %v", err)
		return "", ""
	}
	return cfg.Git.Name, cfg.Git.Email
}
