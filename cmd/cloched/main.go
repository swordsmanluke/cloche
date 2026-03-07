package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/beads"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/adapters/web"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/orchestrator"
	"github.com/cloche-dev/cloche/internal/ports"
	"google.golang.org/grpc"
)

func main() {
	// Load global config file (~/.config/cloche/config)
	globalCfg, err := config.LoadGlobal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load global config: %v\n", err)
		defaults := config.Config{}
		globalCfg = &defaults
	}

	dbPath := envOrConfig("CLOCHE_DB", globalCfg.Daemon.DB, "cloche.db")
	listenAddr := envOrConfig("CLOCHE_LISTEN", globalCfg.Daemon.Listen, "unix:///tmp/cloche.sock")

	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Sweep stale runs from a previous daemon crash (pending or running with no live goroutine).
	if n, err := store.FailStaleRuns(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to sweep stale runs: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "startup: marked %d stale run(s) as failed\n", n)
	}

	runtime, err := initRuntime(globalCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init runtime: %v\n", err)
		os.Exit(1)
	}

	defaultImage := envOrConfig("CLOCHE_IMAGE", globalCfg.Daemon.Image, "cloche-agent:latest")

	broadcaster := logstream.NewBroadcaster()

	srv := adaptgrpc.NewClocheServerWithCaptures(store, store, runtime, defaultImage)
	srv.SetLogStore(store)
	srv.SetLogBroadcaster(broadcaster)

	// Set up evolution trigger
	evoTrigger := initEvolution(globalCfg, store, store)
	if evoTrigger != nil {
		srv.SetEvolution(evoTrigger)
	}

	// Set up merge agent
	srv.SetMergeQueue(store)
	mergeAgent := initMergeAgent(globalCfg, store)
	if mergeAgent != nil {
		srv.SetOnMergeReady(func(ctx context.Context, projectDir string) {
			mergeAgent.ProcessQueue(ctx, projectDir)
		})
	}

	// Set up orchestrator
	orch := initOrchestrator(globalCfg, store, srv)
	if orch != nil {
		srv.SetOnRunComplete(func(ctx context.Context, projectDir string, runID string, state domain.RunState) {
			orch.OnRunComplete(ctx, projectDir, runID, state)
		})
		srv.SetOrchestrateFunc(func(ctx context.Context, projectDir string) (int, error) {
			if projectDir != "" {
				return orch.Run(ctx, projectDir)
			}
			return orch.TriggerAll(ctx), nil
		})
	}

	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, srv)

	srv.SetShutdownFunc(func() { grpcServer.GracefulStop() })

	lis, err := listen(listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen on %s: %v\n", listenAddr, err)
		os.Exit(1)
	}

	var httpServer *http.Server
	if httpAddr := envOrConfig("CLOCHE_HTTP", globalCfg.Daemon.HTTP, ""); httpAddr != "" {
		webOpts := []web.HandlerOption{
			web.WithContainerLogger(runtime),
			web.WithLogStore(store),
			web.WithLogBroadcaster(broadcaster),
		}
		if orch != nil {
			webOpts = append(webOpts, web.WithOrchestrateFunc(func(ctx context.Context, projectDir string) (int, error) {
				return orch.Run(ctx, projectDir)
			}))
			webOpts = append(webOpts, web.WithOrchestrator(orch))
		}
		webHandler, err := web.NewHandler(store, store, webOpts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create web handler: %v\n", err)
			os.Exit(1)
		}
		httpServer = &http.Server{Addr: httpAddr, Handler: webHandler}
		go func() {
			fmt.Fprintf(os.Stderr, "cloched web dashboard on http://%s\n", httpAddr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "http server error: %v\n", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		if httpServer != nil {
			httpServer.Close()
		}
		grpcServer.GracefulStop()
	}()

	// Trigger orchestration on startup
	if orch != nil {
		go func() {
			n := orch.TriggerAll(context.Background())
			if n > 0 {
				fmt.Fprintf(os.Stderr, "startup: orchestrator dispatched %d run(s)\n", n)
			}
		}()
	}

	fmt.Fprintf(os.Stderr, "cloched listening on %s\n", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func initRuntime(cfg *config.Config) (ports.ContainerRuntime, error) {
	runtimeType := envOrConfig("CLOCHE_RUNTIME", cfg.Daemon.Runtime, "docker")

	switch runtimeType {
	case "local":
		agentPath := envOrConfig("CLOCHE_AGENT_PATH", cfg.Daemon.AgentPath, "")
		if agentPath == "" {
			// Look for cloche-agent next to this binary
			exe, err := os.Executable()
			if err == nil {
				agentPath = filepath.Join(filepath.Dir(exe), "cloche-agent")
			} else {
				agentPath = "cloche-agent"
			}
		}
		return local.NewRuntime(agentPath), nil
	case "docker":
		return docker.NewRuntime()
	default:
		return nil, fmt.Errorf("unknown runtime: %s", runtimeType)
	}
}

func initEvolution(globalCfg *config.Config, evoStore ports.EvolutionStore, capStore ports.CaptureStore) *evolution.Trigger {
	// Load config from working directory (daemon-level defaults)
	cfg, err := config.Load(".")
	if err != nil || !cfg.Evolution.Enabled {
		return nil
	}

	llmCmd := envOrConfig("CLOCHE_LLM_COMMAND", globalCfg.Daemon.LLMCommand, "")
	if llmCmd == "" {
		return nil
	}

	trigger := evolution.NewTrigger(evolution.TriggerConfig{
		DebounceSeconds: cfg.Evolution.DebounceSeconds,
		RunFunc: func(projectDir, workflowName, runID string) {
			// Load per-project config for confidence threshold
			projCfg, err := config.Load(projectDir)
			if err != nil {
				projCfg = cfg // fall back to daemon config
			}

			llm := &evolution.CommandLLMClient{Command: llmCmd}
			orch := evolution.NewOrchestrator(evolution.OrchestratorConfig{
				ProjectDir:    projectDir,
				WorkflowName:  workflowName,
				LLM:           llm,
				MinConfidence: projCfg.Evolution.MinConfidence,
			})

			ctx := context.Background()
			if _, err := orch.Run(ctx, runID, evoStore, capStore); err != nil {
				fmt.Fprintf(os.Stderr, "evolution error for %s/%s: %v\n", projectDir, workflowName, err)
			}
		},
	})

	return trigger
}

func listen(addr string) (net.Listener, error) {
	if len(addr) > 7 && addr[:7] == "unix://" {
		sockPath := addr[7:]
		os.Remove(sockPath)
		return net.Listen("unix", sockPath)
	}
	return net.Listen("tcp", addr)
}

func initOrchestrator(globalCfg *config.Config, store ports.RunStore, srv *adaptgrpc.ClocheServer) *orchestrator.Orchestrator {
	// DISABLED: orchestration loop was consuming too many tokens by dispatching
	// runs too aggressively. Re-enable once rate limiting / backpressure is in place.
	return nil

	llmClient := orchestrator.NewCommandLLMClientFromEnv()
	promptGen := &orchestrator.LLMPromptGenerator{LLM: llmClient}

	dispatch := func(ctx context.Context, workflowName, projectDir, prompt string) (string, error) {
		resp, err := srv.RunWorkflow(ctx, &pb.RunWorkflowRequest{
			WorkflowName: workflowName,
			ProjectDir:   projectDir,
			Prompt:       prompt,
		})
		if err != nil {
			return "", err
		}
		return resp.RunId, nil
	}

	waiter := &orchestrator.StoreRunWaiter{Store: store}
	hostRunner := &orchestrator.HostRunner{
		Dispatch: dispatch,
		WaitRun:  waiter,
	}

	orch := orchestrator.New(promptGen, dispatch,
		orchestrator.WithHostRunner(hostRunner),
		orchestrator.WithParseHostWorkflow(dsl.Parse),
	)

	// Collect candidate project directories: cwd + all known projects from the store.
	candidates := map[string]bool{}
	if cwd, err := os.Getwd(); err == nil {
		candidates[cwd] = true
	}
	if projects, err := store.ListProjects(context.Background()); err == nil {
		for _, dir := range projects {
			candidates[dir] = true
		}
	}

	// Register every project that has orchestration enabled.
	registered := 0
	for dir := range candidates {
		cfg, err := config.Load(dir)
		if err != nil || !cfg.Orchestration.Enabled {
			continue
		}
		tracker := beads.NewTracker(dir)
		orch.Register(&orchestrator.ProjectConfig{
			Dir:         dir,
			Workflow:    cfg.Orchestration.Workflow,
			Concurrency: cfg.Orchestration.Concurrency,
			Tracker:     tracker,
		})
		registered++
	}

	if registered == 0 {
		return nil
	}
	return orch
}

func initMergeAgent(globalCfg *config.Config, store ports.MergeQueueStore) *orchestrator.MergeAgent {
	return &orchestrator.MergeAgent{
		MergeQueue: store,
	}
}

// envOrConfig returns the env var value if set, otherwise the config file
// value if non-empty, otherwise the fallback default.
func envOrConfig(envKey, configVal, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if configVal != "" {
		return configVal
	}
	return fallback
}
