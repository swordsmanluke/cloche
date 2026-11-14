package features_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/adapters/web"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/module/qaconformance"
	"github.com/swordsmanluke/cloche/internal/signal"
	"strconv"
)

func init() { registerScenarios(initGenericJudgeScenarios) }

// judgeAskProvider is the daemon's ask slot: it "creates" one question id per
// question in the posted file, without running a module.
type judgeAskProvider struct {
	web.TaskProvider // unused methods; nil
	n                int
}

func (p *judgeAskProvider) RunTrackerPhase(_ context.Context, _ string, _ string, _ map[string]string) (map[string]string, error) {
	ids := make([]string, p.n)
	for i := range ids {
		ids[i] = "Q" + strconv.Itoa(i+1)
	}
	return map[string]string{"question_ids": strings.Join(ids, "\n")}, nil
}

// judgeCtx is per-scenario state: a project with the stub qa module bound, the
// real web handler behind an httptest server, and the real cloche binary.
type judgeCtx struct {
	dir       string
	srv       *httptest.Server
	launcher  *host.JudgeLauncher
	store     *sqlite.Store
	binDir    string // holds the fake `claude` agent
	oldPath   string
	agentFail bool
	gateShut  bool
	stdout    string
	stderr    string
	exitCode  int
	askedN    int
}

func (j *judgeCtx) teardown() {
	if j.oldPath != "" {
		os.Setenv("PATH", j.oldPath)
	}
	if j.binDir != "" {
		os.RemoveAll(j.binDir)
	}
	if j.srv != nil {
		j.srv.Close()
	}
	if j.dir != "" {
		os.RemoveAll(j.dir)
	}
	*j = judgeCtx{}
}

func (j *judgeCtx) setup() error {
	j.teardown()
	dir, err := os.MkdirTemp("", "cloche-judge-")
	if err != nil {
		return err
	}
	j.dir = dir
	modDir, err := qaconformance.InstallStubModule(filepath.Join(dir, ".cloche", "modules"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(modDir, "judge.md")); err != nil {
		return fmt.Errorf("stub module has no judge.md: %w", err)
	}
	// `cloche validate` requires a README in every module.
	if err := os.WriteFile(filepath.Join(modDir, "README.md"), []byte("stub qa module\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".cloche", "project.cloche"), []byte("bind qa = stub\n"), 0o644)
}

// fakeAgentScript is the stand-in `claude`: it records the prompt it was given
// (one file per invocation), reports token usage and ends with the nonce-framed
// result marker. It answers no question.
const fakeAgentScript = `#!/bin/sh
cat > "$FAKE_AGENT_DIR/prompt.$$.txt"
result=success
if [ -f "$FAKE_AGENT_DIR/fail" ]; then result=fail; fi
echo '{"type":"result","usage":{"input_tokens":120,"output_tokens":30},"result":"CLOCHE_RESULT:'"$CLOCHE_RESULT_NONCE:$result"'"}'
`

// installFakeAgent puts a fake `claude` first on PATH for this scenario; the
// real judge launcher (in this process) runs it as the judge's agent.
func (j *judgeCtx) installFakeAgent() error {
	dir, err := os.MkdirTemp("", "cloche-judge-agent-")
	if err != nil {
		return err
	}
	j.binDir = dir
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(fakeAgentScript), 0o755); err != nil {
		return err
	}
	j.oldPath = os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+j.oldPath)
	os.Setenv("FAKE_AGENT_DIR", dir)
	return nil
}

// agentPrompts returns the prompts the fake agent received.
func (j *judgeCtx) agentPrompts() []string {
	files, _ := filepath.Glob(filepath.Join(j.binDir, "prompt.*.txt"))
	var out []string
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			out = append(out, string(b))
		}
	}
	return out
}

// judgeRuns lists the recorded runs of the judge workflow.
func (j *judgeCtx) judgeRuns() ([]*domain.Run, error) {
	runs, err := j.store.ListRunsByProject(context.Background(), j.dir, time.Time{})
	if err != nil {
		return nil, err
	}
	var out []*domain.Run
	for _, r := range runs {
		if r.WorkflowName == signal.JudgeWorkflowName {
			out = append(out, r)
		}
	}
	return out, nil
}

func (j *judgeCtx) exec(args ...string) error {
	bin, err := questionsBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = j.dir
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(j.dir, "home"))
	if j.srv != nil {
		cmd.Env = append(cmd.Env, "CLOCHE_HTTP="+j.srv.URL)
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	j.exitCode = 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			return err
		}
		j.exitCode = ee.ExitCode()
	}
	j.stdout, j.stderr = o.String(), e.String()
	return nil
}

// ask starts the real handler (so the launch decision is the production code)
// and runs the cloche CLI against it.
func (j *judgeCtx) ask(n int, command string) error {
	fields := strings.Fields(command)
	if len(fields) < 3 || fields[0] != "cloche" || fields[1] != "questions" {
		return fmt.Errorf("unsupported command %q", command)
	}
	j.askedN = n
	store, err := sqlite.NewStore(":memory:")
	if err != nil {
		return err
	}
	j.store = store
	if j.agentFail {
		if err := os.WriteFile(filepath.Join(j.binDir, "fail"), nil, 0o644); err != nil {
			return err
		}
	}
	j.launcher = &host.JudgeLauncher{
		NewRunner: func(string) *host.Runner { return &host.Runner{Store: store, Captures: store} },
		Blocked:   func() bool { return j.gateShut },
	}
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = j.dir
	if err := store.CreateRun(context.Background(), run); err != nil {
		return err
	}
	tokens := signal.NewTokenStore(filepath.Join(j.dir, "home", ".config", "cloche", "signal-tokens"))
	h, err := web.NewHandler(store, store, web.WithSignalIntake(tokens, nil),
		web.WithTaskProvider(&judgeAskProvider{n: n}), web.WithJudgeLauncher(j.launcher))
	if err != nil {
		return err
	}
	j.srv = httptest.NewServer(h)

	var body strings.Builder
	body.WriteString("title: T\ncontext: C\nquestions:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&body, "  - prompt: q%d\n", i+1)
	}
	if err := os.WriteFile(filepath.Join(j.dir, "questions.yaml"), []byte(body.String()), 0o644); err != nil {
		return err
	}
	err = j.exec(append(append([]string{}, fields[1:]...), "--file", "questions.yaml")...)
	j.launcher.Wait() // the judge runs in the background; let it finish before asserting
	return err
}

func (j *judgeCtx) onlyPrompt() (string, error) {
	ps := j.agentPrompts()
	if len(ps) != 1 {
		return "", fmt.Errorf("fake agent got %d prompts, want 1", len(ps))
	}
	return ps[0], nil
}

func (j *judgeCtx) wantIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "Q" + strconv.Itoa(i+1)
	}
	return ids
}

// The judge launcher is the production host.JudgeLauncher; only the agent
// (a fake `claude` on PATH) and the budget gate are stand-ins.
func initGenericJudgeScenarios(ctx *godog.ScenarioContext) {
	j := &judgeCtx{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		j.teardown()
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		j.teardown()
		return c, nil
	})

	// Givens
	ctx.Step(`^a project whose qa slot is bound to a module that ships a judge\.md$`, j.setup)
	// L1 only needs the launcher fake; the agent runner and budget gate belong to L2.
	ctx.Step(`^a fake agent runner and an open budget gate$`, j.installFakeAgent)
	ctx.Step(`^\.cloche/config\.toml sets "([^"]*)" "([^"]*)"$`, func(table, line string) error {
		return os.WriteFile(filepath.Join(j.dir, ".cloche", "config.toml"), []byte(table+"\n"+line+"\n"), 0o644)
	})
	// A judge that cannot do its job: the fake agent ends with the fail marker.
	ctx.Step(`^the judge launcher fails$`, func() error { j.agentFail = true; return nil })
	ctx.Step(`^the bound qa module has no judge\.md$`, func() error {
		return os.Remove(filepath.Join(j.dir, ".cloche", "modules", "stub", "judge.md"))
	})
	ctx.Step(`^the budget launch gate is closed$`, func() error { j.gateShut = true; return nil })
	ctx.Step(`^the project defines a host workflow named "([^"]*)"$`, func(n string) error {
		wf := "workflow " + n + " {\n  host {}\n  step mine {\n    run     = \"echo project judge\"\n    results = [success]\n  }\n  mine:success -> done\n}\n"
		return os.WriteFile(filepath.Join(j.dir, ".cloche", n+".cloche"), []byte(wf), 0o644)
	})
	ctx.Step(`^the judge run fails$`, func() error { j.agentFail = true; return nil })
	ctx.Step(`^no judge run is started$`, func() error {
		runs, err := j.judgeRuns()
		if err != nil {
			return err
		}
		if len(runs) != 0 || len(j.agentPrompts()) != 0 {
			return fmt.Errorf("%d judge runs, %d agent invocations; want none", len(runs), len(j.agentPrompts()))
		}
		return nil
	})
	ctx.Step(`^the judge run's prompt contains the rules .*$`, func() error {
		p, err := j.onlyPrompt()
		if err != nil {
			return err
		}
		for _, want := range []string{"Cite your sources", "Never replace a human answer", "leave the question unanswered", "false premise", "permission or approval"} {
			if !strings.Contains(p, want) {
				return fmt.Errorf("prompt lacks %q:\n%s", want, p)
			}
		}
		return nil
	})
	ctx.Step(`^the module's judge\.md text appears after those rules$`, func() error {
		p, err := j.onlyPrompt()
		if err != nil {
			return err
		}
		guide, err := os.ReadFile(filepath.Join(j.dir, ".cloche", "modules", "stub", "judge.md"))
		if err != nil {
			return err
		}
		rules, at := strings.Index(p, "permission or approval"), strings.Index(p, strings.TrimSpace(string(guide)))
		if at < 0 || at < rules {
			return fmt.Errorf("judge.md at %d, rules end near %d:\n%s", at, rules, p)
		}
		return nil
	})
	ctx.Step(`^run history lists a judge run$`, func() error {
		runs, err := j.judgeRuns()
		if err != nil {
			return err
		}
		if len(runs) != 1 {
			return fmt.Errorf("%d judge runs in history, want 1", len(runs))
		}
		return nil
	})
	ctx.Step(`^that run reports token usage$`, func() error {
		runs, _ := j.judgeRuns()
		if len(runs) != 1 {
			return fmt.Errorf("%d judge runs, want 1", len(runs))
		}
		caps, err := j.store.GetCaptures(context.Background(), runs[0].ID)
		if err != nil {
			return err
		}
		for _, c := range caps {
			if c.Usage != nil && c.Usage.InputTokens > 0 {
				return nil
			}
		}
		return fmt.Errorf("no step capture with token usage for run %s", runs[0].ID)
	})
	// The fake agent never answers, so unanswered means the judge run did not
	// succeed (gate closed: no run at all; failing judge: a failed run).
	ctx.Step(`^the questions remain unanswered$`, func() error {
		runs, err := j.judgeRuns()
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.State == domain.RunStateSucceeded {
				return fmt.Errorf("judge run %s succeeded", r.ID)
			}
		}
		return nil
	})
	ctx.Step(`^the project's judge workflow runs instead of the built-in one$`, func() error {
		runs, err := j.judgeRuns()
		if err != nil {
			return err
		}
		if len(runs) != 1 || runs[0].IsBuiltin {
			return fmt.Errorf("want one non-builtin judge run, got %d (%+v)", len(runs), runs)
		}
		if n := len(j.agentPrompts()); n != 0 {
			return fmt.Errorf("built-in agent ran %d times", n)
		}
		return nil
	})

	// Whens
	ctx.Step(`^I ask (\d+) questions with "([^"]*)"$`, j.ask)
	ctx.Step(`^I run "cloche validate"$`, func() error { return j.exec("validate") })

	// Thens
	ctx.Step(`^the (\d+) new question ids are printed$`, func(n int) error {
		if want := strings.Join(j.wantIDs(n), "\n"); strings.TrimSpace(j.stdout) != want {
			return fmt.Errorf("stdout %q, want ids %q (stderr %q)", j.stdout, want, j.stderr)
		}
		return nil
	})
	ctx.Step(`^exactly one judge launch is requested, carrying those (\d+) ids$`, func(n int) error {
		runs, err := j.judgeRuns()
		if err != nil {
			return err
		}
		p, perr := j.onlyPrompt()
		if len(runs) != 1 || perr != nil {
			return fmt.Errorf("%d judge runs (prompt: %v), want exactly one", len(runs), perr)
		}
		for _, id := range j.wantIDs(n) {
			if !strings.Contains(p, "- "+id+"\n") {
				return fmt.Errorf("prompt does not carry id %s:\n%s", id, p)
			}
		}
		return nil
	})
	ctx.Step(`^no judge launch is requested$`, func() error {
		runs, err := j.judgeRuns()
		if err != nil {
			return err
		}
		if len(runs) != 0 {
			return fmt.Errorf("unexpected judge runs %d", len(runs))
		}
		return nil
	})
	ctx.Step(`^the judge ask command exits 0$`, func() error {
		if j.exitCode != 0 {
			return fmt.Errorf("exit %d, stderr %q", j.exitCode, j.stderr)
		}
		return nil
	})
	ctx.Step(`^the validate output warns that the qa module has no judge\.md$`, func() error {
		if !strings.Contains(j.stderr+j.stdout, "no judge.md") {
			return fmt.Errorf("no judge.md warning in %q", j.stderr+j.stdout)
		}
		return nil
	})
	ctx.Step(`^the validate command exits 0$`, func() error {
		if j.exitCode != 0 {
			return fmt.Errorf("exit %d, stderr %q", j.exitCode, j.stderr)
		}
		return nil
	})
	ctx.Step(`^the validate output has no judge\.md warning$`, func() error {
		if strings.Contains(j.stderr+j.stdout, "judge.md") {
			return fmt.Errorf("unexpected judge.md warning in %q", j.stderr+j.stdout)
		}
		return nil
	})
}
