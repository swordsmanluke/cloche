package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	helpslack "github.com/cloche-dev/cloche/internal/adapters/help/slack"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/adapters/web"
	"github.com/cloche-dev/cloche/internal/attention"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/help"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/version"
	"google.golang.org/grpc"
)

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "-v" || os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("cloched %s\n", version.Version())
		return
	}

	var debugAddrFlag string
	flag.StringVar(&debugAddrFlag, "debug-addr", "", "enable pprof debug HTTP server on this address (e.g. localhost:7778)")
	flag.Parse()

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

	// Announce where `cloche shutdown --restart` will redirect stdout/stderr
	// for a future relaunch, so users know where to look even if this
	// process's own output isn't currently going there (e.g. started
	// manually in a terminal).
	fmt.Fprintf(os.Stderr, "startup: daemon restart log location: %s (override with CLOCHE_LOG)\n", config.DefaultLogPath())

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
	srv.SetActivityStore(store)
	srv.SetLogBroadcaster(broadcaster)
	srv.SetContainerPool(docker.NewContainerPool(runtime))

	// Set up the help channel router (AskHelp/ListThreads/GetThread/ReplyThread).
	// The CLI channel (`cloche threads`) is always available and needs no
	// setup here; configured integrations (e.g. Slack) fan out in addition.
	parkAfter := parseDurationOr(globalCfg.Help.ParkAfter, help.DefaultParkAfter)
	retention := parseDurationOr(globalCfg.Help.Retention, help.DefaultRetention)
	helpChannels := initHelpChannels(globalCfg, store)
	helpRouter := help.NewRouter(store, parkAfter, helpChannels...)
	helpRouter.SetParkFunc(srv.ParkRunForHelp)
	helpRouter.SetOnThreadChanged(func(runID string) {
		if run, err := store.GetRun(context.Background(), runID); err == nil && run != nil {
			srv.TriggerAttentionRefresh(run.ProjectDir)
		}
	})
	srv.SetHelpRouter(helpRouter)

	// Start the background attention cache (see internal/attention.Cache):
	// answers /api/projects, GET .../attention, the occupancy summary, and
	// the GetAttention RPC from a per-project cache refreshed on a timer
	// (plus immediately after a run completes or a help thread changes)
	// instead of running each project's list-tasks workflow on every request.
	attentionInterval := parseDurationOr(globalCfg.Attention.RefreshInterval, attention.DefaultRefreshInterval)
	attentionParallel := globalCfg.Attention.MaxParallelRefresh
	attentionCtx, attentionCancel := context.WithCancel(context.Background())
	defer attentionCancel()
	srv.StartAttentionCache(attentionCtx, attentionInterval, attentionParallel)

	// Run each configured HelpChannel's Socket/event loop for the daemon's
	// lifetime. A channel that fails to connect (bad token, network down)
	// only logs — the CLI channel keeps working regardless.
	helpChannelsCtx, helpChannelsCancel := context.WithCancel(context.Background())
	defer helpChannelsCancel()
	for _, ch := range helpChannels {
		go func(c ports.HelpChannel) {
			if err := c.Start(helpChannelsCtx, helpRouter); err != nil && helpChannelsCtx.Err() == nil {
				fmt.Fprintf(os.Stderr, "help channel %s stopped: %v\n", c.Name(), err)
			}
		}(ch)
	}

	// Mint the daemon-lifetime secret used to authenticate the ask_user MCP
	// tool (see internal/mcpauth). Only docker containers get the resulting
	// CLOCHE_MCP_URL/CLOCHE_MCP_TOKEN env vars; the web dashboard must also be
	// enabled (CLOCHE_HTTP) for /mcp to actually be reachable.
	mcpSecret := make([]byte, 32)
	if _, err := rand.Read(mcpSecret); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to generate MCP secret, ask_user tool disabled: %v\n", err)
		mcpSecret = nil
	}
	if dockerRuntime, ok := runtime.(*docker.Runtime); ok && mcpSecret != nil {
		dockerRuntime.SetMCPSecret(mcpSecret)
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
	webCtx, webCancel := context.WithCancel(context.Background())
	defer webCancel()
	if httpAddr := envOrConfig("CLOCHE_HTTP", globalCfg.Daemon.HTTP, ""); httpAddr != "" {
		// Ensure docker.Runtime sees the resolved address even when it only
		// came from the config file, since it reads CLOCHE_HTTP directly to
		// build each container's CLOCHE_MCP_URL.
		os.Setenv("CLOCHE_HTTP", httpAddr)

		webOpts := []web.HandlerOption{
			web.WithContainerLogger(runtime),
			web.WithLogStore(store),
			web.WithLogBroadcaster(broadcaster),
			web.WithTaskProvider(srv),
			web.WithTaskStore(store),
			web.WithAttentionProvider(srv),
			web.WithAttentionMuter(srv),
			web.WithOccupancyProvider(srv),
			web.WithActivityStore(store),
			web.WithOrchestrateFunc(func(ctx context.Context, projectDir string) (int, error) {
				_, err := srv.EnableLoop(ctx, &pb.EnableLoopRequest{ProjectDir: projectDir})
				if err != nil {
					return 0, err
				}
				return 1, nil
			}),
			web.WithLoopStatusFunc(srv.LoopRunning),
			web.WithStopLoopFunc(srv.StopLoop),
			web.WithStopRunFunc(func(ctx context.Context, taskID string) error {
				_, err := srv.StopRun(ctx, &pb.StopRunRequest{TaskId: taskID})
				return err
			}),
			web.WithScanFunc(func(ctx context.Context, projectDir string) (string, error) {
				resp, err := srv.RunWorkflow(ctx, &pb.RunWorkflowRequest{
					WorkflowName: "intent-scan",
					ProjectDir:   projectDir,
				})
				if err != nil {
					return "", err
				}
				return resp.RunId, nil
			}),
			web.WithGetThreadFunc(func(ctx context.Context, address string) (web.ThreadSummary, []web.ThreadMessage, error) {
				resp, err := srv.GetThread(ctx, &pb.GetThreadRequest{Address: address})
				if err != nil {
					return web.ThreadSummary{}, nil, err
				}
				t := resp.Thread
				summary := web.ThreadSummary{
					Address:   t.Channel + "/" + t.Name,
					Title:     t.Title,
					State:     t.State,
					TaskID:    t.TaskId,
					RunID:     t.RunId,
					StepName:  t.StepName,
					CreatedAt: t.CreatedAt,
				}
				msgs := make([]web.ThreadMessage, 0, len(resp.Messages))
				for _, m := range resp.Messages {
					msgs = append(msgs, web.ThreadMessage{
						Author:    m.Author,
						Body:      m.Body,
						Options:   m.Options,
						CreatedAt: m.CreatedAt,
					})
				}
				return summary, msgs, nil
			}),
			// Goes through the exact ReplyThread RPC handler `cloche threads
			// reply` uses, including its resume-if-parked side effect.
			web.WithReplyThreadFunc(func(ctx context.Context, address, body string) error {
				_, err := srv.ReplyThread(ctx, &pb.ReplyThreadRequest{Address: address, Body: body})
				return err
			}),
		}
		if mcpSecret != nil {
			webOpts = append(webOpts, web.WithHelpMCP(mcpSecret, srv.AskHelpForRun))
		}
		webHandler, err := web.NewHandler(store, store, webOpts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create web handler: %v\n", err)
			os.Exit(1)
		}
		httpServer = &http.Server{Addr: httpAddr, Handler: webHandler}
		go serveWebWithRetry(webCtx, httpServer, httpAddr, srv, time.Second, 30*time.Second)
	}

	// Start debug HTTP server if --debug-addr or CLOCHE_DEBUG or [daemon] debug is set.
	if debugAddr := envOrConfig("CLOCHE_DEBUG", globalCfg.Daemon.Debug, debugAddrFlag); debugAddr != "" {
		go startDebugServer(debugAddr, srv)
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

	// Start the daily sweep that deletes archived help threads past retention.
	helpSweepCtx, helpSweepCancel := context.WithCancel(context.Background())
	defer helpSweepCancel()
	go runHelpRetentionSweep(helpSweepCtx, helpRouter, retention)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sigCh:
	case <-shutdownCh:
	}
	webCancel()
	if httpServer != nil {
		httpServer.Close()
	}
	grpcServer.GracefulStop()
}

// serveWebWithRetry binds and serves the web dashboard on addr, retrying
// with exponential backoff on bind or serve failure instead of giving up
// permanently. This lets the dashboard self-heal from a transient port
// conflict (e.g. a hung previous daemon still holding the port during a
// restart) without operator intervention. Status is reported to srv via
// SetWebStatus so `cloche status`/`cloche health` can surface a down
// dashboard instead of looking like a healthy daemon. Returns once ctx is
// canceled or the server is closed intentionally (http.ErrServerClosed).
func serveWebWithRetry(ctx context.Context, httpServer *http.Server, addr string, srv *adaptgrpc.ClocheServer, initialBackoff, maxBackoff time.Duration) {
	backoff := initialBackoff
	for {
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "web dashboard bind failed on %s: %v (retrying in %s)\n", addr, err, backoff)
			srv.SetWebStatus(adaptgrpc.WebStatus{Addr: addr, Up: false, Error: err.Error()})
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		fmt.Fprintf(os.Stderr, "cloched web dashboard on http://%s\n", addr)
		srv.SetWebStatus(adaptgrpc.WebStatus{Addr: addr, Up: true})
		backoff = initialBackoff

		err = httpServer.Serve(lis)
		if ctx.Err() != nil || errors.Is(err, http.ErrServerClosed) {
			return
		}

		fmt.Fprintf(os.Stderr, "web dashboard server error: %v (retrying in %s)\n", err, backoff)
		srv.SetWebStatus(adaptgrpc.WebStatus{Addr: addr, Up: false, Error: err.Error()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff, maxBackoff)
	}
}

// nextBackoff doubles cur, capped at max.
func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		next = max
	}
	return next
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

// initHelpChannels constructs the HelpChannel integrations declared in the
// daemon config's [[help.channel]] entries. An unknown channel type is a
// config mistake and fails daemon start; a known type that fails to
// initialize (e.g. missing token env var) only logs a warning and is
// skipped, since the CLI channel (`cloche threads`) always works regardless.
func initHelpChannels(globalCfg *config.Config, store ports.HelpStore) []ports.HelpChannel {
	var channels []ports.HelpChannel
	for _, chCfg := range globalCfg.Help.Channels {
		switch chCfg.Type {
		case "slack":
			ch, err := helpslack.New(helpslack.Config{
				Channel:     chCfg.Channel,
				TokenEnv:    chCfg.TokenEnv,
				AppTokenEnv: chCfg.AppTokenEnv,
				ChannelMap:  chCfg.ChannelMap,
			}, store)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: slack help channel disabled: %v\n", err)
				continue
			}
			channels = append(channels, ch)
		default:
			fmt.Fprintf(os.Stderr, "failed to start: unknown help channel type %q\n", chCfg.Type)
			os.Exit(1)
		}
	}
	return channels
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

	// Filter to only top-level projects; nested .cloche/ configs would otherwise
	// start duplicate loops that contend for the same task queue.
	projects = filterNestedProjects(projects)

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

		// If the operator explicitly stopped the loop before this restart, do not
		// auto-enable it. The flag file is written by DisableLoop and cleared by EnableLoop.
		if adaptgrpc.LoopWasExplicitlyStopped(projectDir) {
			fmt.Fprintf(os.Stderr, "startup: loop was explicitly stopped for %s — skipping auto-enable\n", projectDir)
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

// filterNestedProjects removes paths that are subdirectories of other paths in
// the list. When a project root contains nested .cloche/ configs (vendored
// repos, cloned projects), this prevents launching separate orchestration
// loops that would contend for the same task queue.
func filterNestedProjects(dirs []string) []string {
	sorted := make([]string, len(dirs))
	copy(sorted, dirs)
	sort.Strings(sorted)

	var result []string
	for _, dir := range sorted {
		nested := false
		for _, existing := range result {
			if projectIsSubpath(existing, dir) {
				nested = true
				fmt.Fprintf(os.Stderr, "startup: skipping nested project %s (already covered by %s)\n", dir, existing)
				break
			}
		}
		if !nested {
			result = append(result, dir)
		}
	}
	return result
}

// projectIsSubpath reports whether child is strictly inside parent on a
// path-separator boundary (/root/a is inside /root, but /root/ab is not).
func projectIsSubpath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
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

// parseDurationOr parses s as a duration, falling back to def if s is empty
// or invalid.
func parseDurationOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: invalid duration %q, using %s: %v\n", s, def, err)
		return def
	}
	return d
}

// runHelpRetentionSweep periodically deletes archived help threads older
// than retention. Runs once immediately, then once a day until ctx is done.
func runHelpRetentionSweep(ctx context.Context, router *help.Router, retention time.Duration) {
	sweep := func() {
		if n, err := router.SweepRetention(context.Background(), retention); err != nil {
			fmt.Fprintf(os.Stderr, "warning: help retention sweep failed: %v\n", err)
		} else if n > 0 {
			fmt.Fprintf(os.Stderr, "startup: deleted %d retention-expired help thread(s)\n", n)
		}
	}
	sweep()

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// startDebugServer starts a pprof + daemon-state HTTP server on addr.
// It registers standard net/http/pprof endpoints plus a /debug/state endpoint
// that returns a JSON snapshot of goroutines, active runs, loops, and container
// sessions. The server runs until the process exits.
func startDebugServer(addr string, srv *adaptgrpc.ClocheServer) {
	mux := http.NewServeMux()

	// Standard pprof endpoints (goroutine dumps, heap, CPU, trace, etc.)
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// Daemon state snapshot as JSON.
	mux.HandleFunc("/debug/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := srv.DaemonState()
		if err := json.NewEncoder(w).Encode(snap); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	fmt.Fprintf(os.Stderr, "cloched debug server on http://%s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "debug server error: %v\n", err)
	}
}
