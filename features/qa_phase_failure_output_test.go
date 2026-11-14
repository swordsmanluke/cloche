package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initQAPhaseFailureOutputScenarios) }

// fakePhaseResult is the daemon's phase-failure payload, faked until L2
// captures the real step output. TODO(L2): drop it once the real daemon
// supplies failed_step / output_tail.
type fakePhaseResult struct {
	Error      string `json:"error"`
	FailedStep string `json:"failed_step"`
	OutputTail string `json:"output_tail"`
}

type qaPhaseFailure struct {
	q       questionsCLI
	msg     string
	output  string // full output the failing script printed
	fakeSrv *httptest.Server
}

func initQAPhaseFailureOutputScenarios(ctx *godog.ScenarioContext) {
	f := &qaPhaseFailure{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*f = qaPhaseFailure{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if f.fakeSrv != nil {
			f.fakeSrv.Close()
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

	// Steps are pending until the named layer implements them. Do not return a
	// plain error here: that would fail go test for every layer above.
	ctx.Step(`^the qa module's phase scripts succeed$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)" against the real daemon$`, func(cmd string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)" from a workflow run$`, func(cmd string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output command exits 0$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output error does not contain "([^"]*)"$`, func(s string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the calling run's step log file for the failing step contains "([^"]*)"$`, func(s string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^"cloche (logs|poll)" for the calling run contains "([^"]*)"$`, func(cmd, s string) error {
		return godog.ErrPending // pending: L2
	})
}

// run drives the real cloche binary against a daemon that answers the route
// with a failed phase carrying fakePhaseResult.
func (f *qaPhaseFailure) run(cmdline string) error {
	args := strings.Fields(strings.TrimPrefix(cmdline, "cloche questions "))
	sub := args[0]
	var rest []string
	for _, a := range args[1:] {
		if a != "--id" { // the CLI takes ids positionally
			rest = append(rest, a)
		}
	}
	step := map[string]string{"ask": "ask", "await": "await-answer", "read": "read-answer"}[sub]
	if step == "" {
		return fmt.Errorf("unsupported subcommand %q", sub)
	}
	if sub == "ask" {
		if err := f.q.writeFile("title: T\ncontext: C\nquestions:\n  - prompt: one\n"); err != nil {
			return err
		}
	}
	token := f.q.daemon.token
	f.q.srv.Close()
	f.fakeSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound) // what the daemon answers for ErrPhaseFailed
		// A failed write surfaces as a missing stderr assertion in the scenario;
		// the handler has no other channel to report it.
		_ = json.NewEncoder(w).Encode(fakePhaseResult{
			Error:      fmt.Sprintf("%s: %s workflow finished with state failed: phase workflow failed", step, step),
			FailedStep: step,
			OutputTail: f.output,
		})
	}))
	f.q.srv = f.fakeSrv
	if sub != "ask" {
		if err := os.MkdirAll(filepath.Join(f.q.dir, ".cloche"), 0o755); err != nil {
			return err
		}
	}
	return f.q.run(append([]string{sub}, rest...)...)
}
