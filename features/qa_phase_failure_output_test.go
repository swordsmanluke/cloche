package features_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/adapters/web"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/signal"
)

func init() { registerScenarios(initQAPhaseFailureOutputScenarios) }

type qaPhaseFailure struct {
	q       questionsCLI
	msg     string
	output  string // full output the failing script printed
	succeed bool   // the phase scripts succeed instead of failing
	srv     *httptest.Server
	taskID  string // the calling run, when the CLI runs from a workflow run
	attempt string
}

func initQAPhaseFailureOutputScenarios(ctx *godog.ScenarioContext) {
	f := &qaPhaseFailure{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*f = qaPhaseFailure{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if f.srv != nil {
			f.srv.Close()
		}
		f.q.teardown()
		return c, nil
	})
	ctx.Step(`^a project whose bound qa module has phase scripts that fail with "([^"]*)"$`, func(msg string) error {
		f.msg, f.output = msg, msg+"\n"
		return f.q.setup(true)
	})
	ctx.Step(`^the failing script prints (\d+) lines before "([^"]*)"$`, func(n int, msg string) error {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "noise line %d\n", i)
		}
		f.output = b.String() + msg + "\n"
		return nil
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)"$`, f.run)
	ctx.Step(`^the qa phase failure output command exits non-zero$`, func() error {
		if f.q.exitCode == 0 {
			return fmt.Errorf("expected non-zero exit; stdout %q", f.q.stdout)
		}
		return nil
	})
	ctx.Step(`^the qa phase failure output error contains "([^"]*)"$`, f.q.stderrHas)
	ctx.Step(`^the qa phase failure output error names the failing step "([^"]*)"$`, func(step string) error {
		return f.q.stderrHas("failing step: " + step)
	})
	ctx.Step(`^the qa phase failure output error is shorter than the full script output$`, func() error {
		if len(f.q.stderr) >= len(f.output) {
			return fmt.Errorf("stderr (%d bytes) is not shorter than the script output (%d bytes)", len(f.q.stderr), len(f.output))
		}
		return nil
	})

	ctx.Step(`^the qa module's phase scripts succeed$`, func() error {
		f.succeed = true
		return nil
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)" against the real daemon$`, f.run)
	ctx.Step(`^the qa phase failure output user runs "([^"]*)" from a workflow run$`, func(cmd string) error {
		f.taskID, f.attempt = "T1", "A1"
		if err := os.MkdirAll(f.attemptDir(), 0o755); err != nil {
			return err
		}
		return f.run(cmd)
	})
	ctx.Step(`^the qa phase failure output command exits 0$`, func() error {
		if f.q.exitCode != 0 {
			return fmt.Errorf("exit %d; stderr %q", f.q.exitCode, f.q.stderr)
		}
		return nil
	})
	ctx.Step(`^the qa phase failure output error does not contain "([^"]*)"$`, func(s string) error {
		if strings.Contains(f.q.stderr, s) {
			return fmt.Errorf("stderr %q contains %q", f.q.stderr, s)
		}
		return nil
	})
	ctx.Step(`^the calling run's step log file for the failing step contains "([^"]*)"$`, func(s string) error {
		return fileHas(filepath.Join(f.attemptDir(), "ask.log"), s)
	})
	// `cloche logs` and `cloche poll` for a host run both read the attempt's
	// full.log (web fullLogPath).
	ctx.Step(`^"cloche (logs|poll)" for the calling run contains "([^"]*)"$`, func(_, s string) error {
		return fileHas(filepath.Join(f.attemptDir(), "full.log"), s)
	})
}

func (f *qaPhaseFailure) attemptDir() string {
	return filepath.Join(f.q.dir, ".cloche", "logs", f.taskID, f.attempt)
}

func fileHas(path, sub string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), sub) {
		return fmt.Errorf("%s does not contain %q:\n%s", path, sub, data)
	}
	return nil
}

// qaPhaseProvider runs qa phases through the real host runner, as the daemon's
// ClocheServer.RunTrackerPhase does.
type qaPhaseProvider struct {
	web.TaskProvider // unused methods; nil
	store            *sqlite.Store
}

func (p *qaPhaseProvider) RunTrackerPhase(ctx context.Context, dir, slot string, in map[string]string) (map[string]string, error) {
	return host.RunQuestionPhase(ctx, &host.Runner{Store: p.store}, dir, slot, in)
}

// writeWorkflows defines the three qa phases as project workflows whose steps
// (named after the slot) print the script output and fail, or succeed.
func (f *qaPhaseFailure) writeWorkflows() error {
	outFile := filepath.Join(f.q.dir, "script-output.txt")
	if err := os.WriteFile(outFile, []byte(f.output), 0o644); err != nil {
		return err
	}
	var b strings.Builder
	for _, w := range []struct{ slot, in string }{{"ask", "questions_file"}, {"await-answer", "question_ids"}, {"read-answer", "question_id"}} {
		run := "cat " + outFile + "; exit 1"
		if f.succeed {
			run = "true"
		}
		fmt.Fprintf(&b, "workflow %s {\n  host {}\n  contract {\n    kv: %s\n  }\n  step %s {\n    run = %q\n    results = [success, fail]\n  }\n  %s:success -> done\n  %s:fail -> abort\n}\n",
			w.slot, w.in, w.slot, run, w.slot, w.slot)
	}
	if err := os.MkdirAll(filepath.Join(f.q.dir, ".cloche"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.q.dir, ".cloche", "qa.cloche"), []byte(b.String()), 0o644)
}

// run drives the real cloche binary against the real web handler and host
// runner, with the project's phase scripts failing (or succeeding).
func (f *qaPhaseFailure) run(cmdline string) error {
	args := strings.Fields(strings.TrimPrefix(cmdline, "cloche questions "))
	var rest []string
	for _, a := range args[1:] { // the CLI takes ids positionally
		if a != "--id" {
			rest = append(rest, a)
		}
	}
	if args[0] == "ask" {
		if err := f.q.writeFile("title: T\ncontext: C\nquestions:\n  - prompt: one\n"); err != nil {
			return err
		}
	}
	if err := f.writeWorkflows(); err != nil {
		return err
	}
	store, err := sqlite.NewStore(":memory:")
	if err != nil {
		return err
	}
	if err := store.CreateRun(context.Background(), func() *domain.Run {
		r := domain.NewRun("run-1", "develop")
		r.ProjectDir = f.q.dir
		return r
	}()); err != nil {
		return err
	}
	tokens := signal.NewTokenStore(filepath.Join(f.q.dir, "home", ".config", "cloche", "signal-tokens"))
	h, err := web.NewHandler(store, store, web.WithSignalIntake(tokens, nil), web.WithTaskProvider(&qaPhaseProvider{store: store}))
	if err != nil {
		return err
	}
	f.q.srv.Close()
	f.srv = httptest.NewServer(h)
	f.q.srv = f.srv
	if f.taskID != "" {
		f.q.extraEnv = []string{"CLOCHE_TASK_ID=" + f.taskID, "CLOCHE_ATTEMPT_ID=" + f.attempt}
	}
	return f.q.run(append([]string{args[0]}, rest...)...)
}
