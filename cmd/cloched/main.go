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
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/version"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/adapters/web"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
	"google.golang.org/grpc"
)

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Printf("cloched %s\n", version.Version())
		return
	}

	// Load global config file (~/.config/cloche/config)
	globalCfg, err := config.LoadGlobal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load global config: %v\n", err)
		defaults := config.Config{}
		globalCfg = &defaults
	}

	// Ensure state directory exists (~/.config/cloche/)
	if _, err := config.EnsureStateDir(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create state dir: %v\n", err)
	}

	dbPath := envOrConfig("CLOCHE_DB", globalCfg.Daemon.DB, config.DefaultDBPath())
	listenAddr := envOrConfig("CLOCHE_ADDR", globalCfg.Daemon.Listen, config.DefaultAddr())

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

	// Sweep stale attempts whose goroutines were killed before completeAttempt could run.
	if n, err := store.FailStaleAttempts(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to sweep stale attempts: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "startup: marked %d stale attempt(s) as failed\n", n)
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
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)
	srv.SetActivityStore(store)
	srv.SetLogBroadcaster(broadcaster)

	// Set up evolution trigger
	evoTrigger := initEvolution(globalCfg, store, store)
	if evoTrigger != nil {
		srv.SetEvolution(evoTrigger)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, srv)

	shutdownCh := make(chan struct{}, 1)
	srv.SetShutdownFunc(func() {
		grpcServer.GracefulStop()
		select {
		case shutdownCh <- struct{}{}:
		default:
		}
	})

	lis, err := net.Listen("tcp", listenAddr)
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
			web.WithTaskProvider(srv),
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

	fmt.Fprintf(os.Stderr, "cloched listening on %s\n", listenAddr)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
	}()

	// Auto-execute main workflow for active projects (after gRPC is serving).
	autoRunActiveProjects(store, srv)

	// Start background scanner that detects workflows stuck in "running" state
	// due to undetected container exits (crashes, OOM kills, etc.).
	scanCtx, scanCancel := context.WithCancel(context.Background())
	defer scanCancel()
	srv.StartStuckWorkflowScanner(scanCtx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sigCh:
	case <-shutdownCh:
	}
	if httpServer != nil {
		httpServer.Close()
	}
	grpcServer.GracefulStop()
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


// autoRunActiveProjects scans known projects for active = true in their config
// and starts the orchestration loop for each one via EnableLoop.
func autoRunActiveProjects(store ports.RunStore, srv *adaptgrpc.ClocheServer) {
	ctx := context.Background()
	projects, err := store.ListProjects(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup: failed to list projects: %v\n", err)
		return
	}

	for _, projectDir := range projects {
		cfg, err := config.Load(projectDir)
		if err != nil || !cfg.Active {
			continue
		}

		// Verify host.cloche exists before launching
		hostPath := filepath.Join(projectDir, ".cloche", "host.cloche")
		if _, err := os.Stat(hostPath); err != nil {
			fmt.Fprintf(os.Stderr, "startup: skipping active project %s: %v\n", projectDir, err)
			continue
		}

		fmt.Fprintf(os.Stderr, "startup: enabling orchestration loop for active project %s\n", projectDir)
		_, err = srv.EnableLoop(ctx, &pb.EnableLoopRequest{
			ProjectDir: projectDir,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "startup: failed to enable loop for %s: %v\n", projectDir, err)
		}
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
