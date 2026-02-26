package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/cloche-dev/cloche/internal/ports"
	"google.golang.org/grpc"
)

func main() {
	dbPath := os.Getenv("CLOCHE_DB")
	if dbPath == "" {
		dbPath = "cloche.db"
	}

	listenAddr := os.Getenv("CLOCHE_LISTEN")
	if listenAddr == "" {
		listenAddr = "unix:///tmp/cloche.sock"
	}

	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Sweep stale pending runs from a previous daemon crash.
	if n, err := store.FailPendingRuns(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to sweep pending runs: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "startup: marked %d stale pending run(s) as failed\n", n)
	}

	runtime, err := initRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init runtime: %v\n", err)
		os.Exit(1)
	}

	defaultImage := os.Getenv("CLOCHE_IMAGE")
	if defaultImage == "" {
		defaultImage = "cloche-agent:latest"
	}

	srv := adaptgrpc.NewClocheServerWithCaptures(store, store, runtime, defaultImage)

	// Set up evolution trigger
	evoTrigger := initEvolution(store, store)
	if evoTrigger != nil {
		srv.SetEvolution(evoTrigger)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, srv)

	srv.SetShutdownFunc(func() { grpcServer.GracefulStop() })

	lis, err := listen(listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen on %s: %v\n", listenAddr, err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		grpcServer.GracefulStop()
	}()

	fmt.Fprintf(os.Stderr, "cloched listening on %s\n", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func initRuntime() (ports.ContainerRuntime, error) {
	runtimeType := os.Getenv("CLOCHE_RUNTIME")
	if runtimeType == "" {
		runtimeType = "docker"
	}

	switch runtimeType {
	case "local":
		agentPath := os.Getenv("CLOCHE_AGENT_PATH")
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

func initEvolution(evoStore ports.EvolutionStore, capStore ports.CaptureStore) *evolution.Trigger {
	// Load config from working directory (daemon-level defaults)
	cfg, err := config.Load(".")
	if err != nil || !cfg.Evolution.Enabled {
		return nil
	}

	llmCmd := os.Getenv("CLOCHE_LLM_COMMAND")
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
