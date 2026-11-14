package features_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/adapters/web"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/draauth"
)

func init() { registerScenarios(initDRAWaitsScenarios) }

// The real cloche binary runs in DRA mode against the real DRA HTTP API
// (internal/adapters/web) served in-process, backed by the real wait registry
// and the real file-backed wait store (dra.FileWaitStore). Poll outcomes are
// scripted (dra.FakePollRunner): running a poll as the recorded host run
// dra-wait-<name> needs the daemon and is covered in internal/adapters/grpc.
type draWaitsCLI struct {
	dir            string
	srv            *httptest.Server
	store          *sqlite.Store
	reg            *dra.WaitRegistry
	runner         *dra.FakePollRunner
	inbox          *dra.Inbox
	now            time.Time
	token          string
	stdout, stderr string
	exitCode       int
	lastID         string
	ids            map[string]string // poll -> wait id
	taken          []dra.Event       // events drained from the inbox
	message        string
	seenFile       string
	pwned          string
}

// waitsAdapter lets the web handler call the registry the way the daemon does.
type waitsAdapter struct{ reg *dra.WaitRegistry }

func (a waitsAdapter) RegisterDRAWait(_ context.Context, p, n string, r dra.WaitRequest) (string, error) {
	return a.reg.Register(p, n, r)
}
func (a waitsAdapter) ListDRAWaits(_ context.Context, p, n string) ([]dra.Wait, error) {
	return a.reg.List(p, n)
}
func (a waitsAdapter) CancelDRAWait(_ context.Context, p, n, id string) error {
	return a.reg.Cancel(p, n, id)
}

var waitIDRe = regexp.MustCompile(`w-\d+`)

// shellSplit splits on spaces, honouring single and double quotes.
func shellSplit(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	have := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, have = r, true
		case r == ' ':
			if have || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if have || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func (c *draWaitsCLI) run(command string) error {
	args := shellSplit(command)
	if len(args) == 0 || args[0] != "cloche" {
		return fmt.Errorf("command %q does not start with cloche", command)
	}
	return c.runArgs(args[1:]...)
}

func (c *draWaitsCLI) runArgs(args ...string) error {
	bin, err := questionsBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "CLOCHE_DRA_URL="+c.srv.URL, "CLOCHE_DRA_TOKEN="+c.token)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	c.exitCode = 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			return err
		}
		c.exitCode = ee.ExitCode()
	}
	c.stdout, c.stderr = so.String(), se.String()
	if m := waitIDRe.FindString(c.stdout); m != "" && c.exitCode == 0 && strings.Contains(c.stdout, "Registered") {
		c.lastID = m
		if len(args) > 1 && args[0] == "dra" && args[1] == "wait" && len(args) > 2 {
			c.ids[args[2]] = m
		}
	}
	return nil
}

func (c *draWaitsCLI) register(args ...string) error {
	if err := c.runArgs(append([]string{"dra", "wait"}, args...)...); err != nil {
		return err
	}
	if c.exitCode != 0 {
		return fmt.Errorf("registering %v failed: %s", args, c.stderr)
	}
	return nil
}

// drain moves every pending inbox event into c.taken, as a wake-up would.
func (c *draWaitsCLI) drain() []dra.Event {
	ev := c.inbox.Take(c.dir, "qa")
	c.taken = append(c.taken, ev...)
	return ev
}

// newRegistry builds a registry over a fresh file store reading the project's
// waits file, as the daemon does when it starts.
func (c *draWaitsCLI) newRegistry() {
	store := dra.NewFileWaitStore(func() []string { return []string{c.dir} })
	c.reg = dra.NewWaitRegistry(store, c.runner, func(p, n string, e dra.Event) { c.inbox.Add(p, n, e) })
	c.reg.SetClock(func() time.Time { return c.now })
}

func (c *draWaitsCLI) tick() { c.reg.Tick(context.Background()) }

func (c *draWaitsCLI) waitEvents() []dra.Event {
	var out []dra.Event
	for _, e := range c.taken {
		if e.Kind == dra.KindWait {
			out = append(out, e)
		}
	}
	return out
}

func (c *draWaitsCLI) eventFor(poll string) (dra.Event, bool) {
	for _, e := range c.waitEvents() {
		if strings.Contains(e.Text, "(poll "+poll+")") {
			return e, true
		}
	}
	return dra.Event{}, false
}

func (c *draWaitsCLI) waitsOutput() (string, error) {
	if err := c.run("cloche dra waits"); err != nil {
		return "", err
	}
	if c.exitCode != 0 {
		return "", fmt.Errorf("cloche dra waits failed: %s", c.stderr)
	}
	return c.stdout, nil
}

func (c *draWaitsCLI) lineFor(out string) (string, error) {
	for _, l := range strings.Split(out, "\n") {
		if c.lastID != "" && strings.HasPrefix(l, c.lastID+" ") {
			return l, nil
		}
	}
	return "", fmt.Errorf("wait %q not listed in:\n%s", c.lastID, out)
}

func initDRAWaitsScenarios(ctx *godog.ScenarioContext) {
	c := &draWaitsCLI{}
	ctx.Before(func(cx context.Context, _ *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "dra-waits-")
		if err != nil {
			return cx, err
		}
		store, err := sqlite.NewStore(":memory:")
		if err != nil {
			return cx, err
		}
		secret := []byte("dra-waits-secret")
		run := domain.NewRun("wake-1", "dra-wake-qa")
		run.ProjectDir = dir
		run.State = domain.RunStateRunning
		if err := store.CreateRun(cx, run); err != nil {
			return cx, err
		}
		*c = draWaitsCLI{dir: dir, store: store, ids: map[string]string{}, runner: dra.NewFakePollRunner(),
			inbox: dra.NewInbox(), now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			seenFile: filepath.Join(dir, "seen-branch"), pwned: filepath.Join(dir, "pwned"),
			token: draauth.Token(secret, draauth.Claims{RunID: "wake-1", Project: dir, DRA: "qa"})}
		c.newRegistry()
		h, err := web.NewHandler(store, store, web.WithDRAAccess(secret), web.WithDRAWaits(waitsAdapter{c.reg}))
		if err != nil {
			return cx, err
		}
		c.srv = httptest.NewServer(h)
		return cx, nil
	})
	ctx.After(func(cx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		c.srv.Close()
		c.store.Close()
		os.RemoveAll(c.dir)
		return cx, nil
	})
	ctx.Step(`^a DRA "([^"]*)" whose project owner has written the polls "([^"]*)" and "([^"]*)"$`, func(name, a, b string) error {
		if err := os.MkdirAll(dra.PollsDir(c.dir, name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dra.Root(c.dir), name, dra.CharterFile), []byte("# QA\n"), 0o644); err != nil {
			return err
		}
		script := "#!/bin/sh\nprintf %s \"$CLOCHE_WAIT_ARG_BRANCH\" > " + c.seenFile + "\n"
		for _, p := range []string{a, b} {
			if err := os.WriteFile(filepath.Join(dra.PollsDir(c.dir, name), p+dra.PollScriptExt), []byte(script), 0o755); err != nil {
				return err
			}
		}
		return nil
	})
	ctx.Step(`^the DRA runs "([^"]*)"$`, func(cmd string) error {
		return c.run(cmd)
	})
	ctx.Step(`^the command succeeds and prints a wait id$`, func() error {
		if c.exitCode != 0 || !strings.Contains(c.stdout, "Registered wait ") || waitIDRe.FindString(c.stdout) == "" {
			return fmt.Errorf("exit %d, stdout %q, stderr %q", c.exitCode, c.stdout, c.stderr)
		}
		return nil
	})
	ctx.Step(`^"([^"]*)" lists that wait with its poll name and note$`, func(_ string) error {
		out, err := c.waitsOutput()
		if err != nil {
			return err
		}
		l, err := c.lineFor(out)
		if err != nil {
			return err
		}
		for _, want := range []string{"build-done", "main build"} {
			if !strings.Contains(l, want) {
				return fmt.Errorf("%q missing from %q", want, l)
			}
		}
		return nil
	})
	ctx.Step(`^"([^"]*)" lists that wait as a questions wait on Q14 and Q15$`, func(_ string) error {
		out, err := c.waitsOutput()
		if err != nil {
			return err
		}
		l, err := c.lineFor(out)
		if err != nil {
			return err
		}
		if !strings.Contains(l, "questions (Q14, Q15)") {
			return fmt.Errorf("not a questions wait on Q14, Q15: %q", l)
		}
		return nil
	})
	ctx.Step(`^the command fails with "([^"]*)"$`, func(want string) error {
		if c.exitCode == 0 || !strings.Contains(c.stderr, want) {
			return fmt.Errorf("want failure containing %q; exit %d, stderr %q", want, c.exitCode, c.stderr)
		}
		return nil
	})
	ctx.Step(`^the refusal lists the polls "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		if !strings.Contains(c.stderr, "this DRA has: ") || !strings.Contains(c.stderr, a) || !strings.Contains(c.stderr, b) {
			return fmt.Errorf("refusal does not list %q and %q: %q", a, b, c.stderr)
		}
		return nil
	})
	ctx.Step(`^no wait is registered$`, func() error {
		out, err := c.waitsOutput()
		if err != nil {
			return err
		}
		if !strings.Contains(out, "No pending waits") {
			return fmt.Errorf("waits remain: %s", out)
		}
		return nil
	})
	ctx.Step(`^the DRA has registered a wait on "([^"]*)"$`, func(poll string) error {
		return c.register(poll, "--note", "test wait")
	})
	ctx.Step(`^the DRA runs "([^"]*)" with that wait's id$`, func(cmd string) error {
		if c.lastID == "" {
			return fmt.Errorf("no wait was registered")
		}
		return c.run(cmd + " " + c.lastID)
	})
	ctx.Step(`^"([^"]*)" no longer lists that wait$`, func(_ string) error {
		out, err := c.waitsOutput()
		if err != nil {
			return err
		}
		if _, err := c.lineFor(out); err == nil {
			return fmt.Errorf("wait %s still listed:\n%s", c.lastID, out)
		}
		return nil
	})
	ctx.Step(`^no wait event is delivered for it$`, func() error {
		c.runner.Script("build-done", dra.PollOutcome{Result: "green"})
		c.tick()
		if ev := c.drain(); len(ev) != 0 {
			return fmt.Errorf("events for a cancelled wait: %+v", ev)
		}
		return nil
	})
	ctx.Step(`^the DRA has registered a wait on "([^"]*)" with note "([^"]*)"$`, func(poll, note string) error {
		return c.register(poll, "--note", note)
	})
	ctx.Step(`^the poll script prints a CLOCHE_RESULT marker named "([^"]*)"$`, func(name string) error {
		c.runner.Script("build-done", dra.PollOutcome{Result: name, Output: "build 7 passed\n"})
		c.tick()
		return nil
	})
	ctx.Step(`^the DRA's next wake-up message contains exactly one wait event$`, func() error {
		c.drain()
		c.message = dra.FormatMessage("qa", c.taken)
		if n := len(c.waitEvents()); n != 1 || strings.Count(c.message, " wait: ") != 1 {
			return fmt.Errorf("want one wait event, got %d in:\n%s", n, c.message)
		}
		return nil
	})
	ctx.Step(`^that event carries the note "([^"]*)", the result name "([^"]*)" and the poll's last output$`, func(note, result string) error {
		for _, want := range []string{note, "result " + result, "build 7 passed"} {
			if !strings.Contains(c.message, want) {
				return fmt.Errorf("%q missing from:\n%s", want, c.message)
			}
		}
		return nil
	})
	ctx.Step(`^the poll script exits 0 without printing a marker$`, func() error {
		c.runner.Script("build-done", dra.PollOutcome{Output: "still building"})
		c.tick()
		return nil
	})
	ctx.Step(`^no wait event is delivered$`, func() error {
		if ev := c.drain(); len(ev) != 0 {
			return fmt.Errorf("unexpected events: %+v", ev)
		}
		return nil
	})
	ctx.Step(`^the wait is still listed as pending$`, func() error {
		out, err := c.waitsOutput()
		if err != nil {
			return err
		}
		_, err = c.lineFor(out)
		return err
	})
	ctx.Step(`^the DRA has registered a wait on "([^"]*)" and another on "([^"]*)" with a 1h timeout$`, func(a, b string) error {
		if err := c.register(a, "--note", "first"); err != nil {
			return err
		}
		return c.register(b, "--note", "second", "--timeout", "1h")
	})
	ctx.Step(`^the "([^"]*)" poll script exits non-zero$`, func(poll string) error {
		c.runner.Script(poll, dra.PollOutcome{Failed: true, Output: "compile error"})
		c.tick()
		return nil
	})
	ctx.Step(`^the "([^"]*)" wait passes its timeout$`, func(poll string) error {
		c.runner.Script(poll, dra.PollOutcome{Output: "still deploying"})
		c.now = c.now.Add(time.Hour)
		c.tick()
		return nil
	})
	ctx.Step(`^the DRA receives a wait event reporting failure for "([^"]*)"$`, func(poll string) error {
		c.drain()
		e, ok := c.eventFor(poll)
		if !ok || !strings.Contains(e.Text, "failed") || !strings.Contains(e.Text, "compile error") {
			return fmt.Errorf("no failure event for %s in %+v", poll, c.taken)
		}
		return nil
	})
	ctx.Step(`^the DRA receives a wait event reporting timeout for "([^"]*)"$`, func(poll string) error {
		c.drain()
		e, ok := c.eventFor(poll)
		if !ok || !strings.Contains(e.Text, "timeout") {
			return fmt.Errorf("no timeout event for %s in %+v", poll, c.taken)
		}
		if n := len(c.waitEvents()); n != 2 {
			return fmt.Errorf("want one event per ended wait (2), got %d", n)
		}
		return nil
	})
	ctx.Step(`^the DRA has an unread inbox event of another kind$`, func() error {
		c.inbox.Add(c.dir, "qa", dra.Event{Kind: dra.KindTaskFailed, Subject: "t-1", Text: "task failed"})
		return nil
	})
	ctx.Step(`^the DRA has a wait that becomes ready$`, func() error {
		if err := c.register("build-done", "--note", "main build"); err != nil {
			return err
		}
		c.runner.Script("build-done", dra.PollOutcome{Result: "green", Output: "ok"})
		c.tick()
		return nil
	})
	ctx.Step(`^the DRA is next woken$`, func() error {
		c.drain()
		c.message = dra.FormatMessage("qa", c.taken)
		return nil
	})
	ctx.Step(`^both events arrive together in one wake-up message$`, func() error {
		for _, want := range []string{"2 events, delivered together", "task-failed", "wait: "} {
			if !strings.Contains(c.message, want) {
				return fmt.Errorf("%q missing from:\n%s", want, c.message)
			}
		}
		return nil
	})
	ctx.Step(`^the DRA registers a wait on "([^"]*)" with the argument "([^"]*)"$`, func(poll, arg string) error {
		// The scenario's /tmp/pwned stands for a path inside the test's own dir.
		arg = strings.ReplaceAll(arg, "/tmp/pwned", c.pwned)
		return c.register(poll, "--arg", arg, "--note", "args")
	})
	ctx.Step(`^the poll runs$`, func() error {
		waits, err := c.reg.List(c.dir, "qa")
		if err != nil || len(waits) != 1 {
			return fmt.Errorf("want one wait, got %v %v", waits, err)
		}
		spec, err := dra.ResolvePoll(c.dir, "qa", waits[0].WaitRequest)
		if err != nil {
			return err
		}
		return spec.Command(context.Background()).Run()
	})
	ctx.Step(`^the script sees CLOCHE_WAIT_ARG_BRANCH holding that exact text$`, func() error {
		got, err := os.ReadFile(c.seenFile)
		if err != nil {
			return err
		}
		want := "main; touch " + c.pwned
		if string(got) != want {
			return fmt.Errorf("script saw %q, want %q", got, want)
		}
		return nil
	})
	ctx.Step(`^the host did not execute "([^"]*)"$`, func(_ string) error {
		if _, err := os.Stat(c.pwned); err == nil {
			return fmt.Errorf("the host executed text from a poll argument")
		}
		return nil
	})
	ctx.Step(`^the DRA already has 20 pending waits$`, func() error {
		for i := 0; i < dra.MaxPendingWaits; i++ {
			if _, err := c.reg.Register(c.dir, "qa", dra.WaitRequest{Poll: "build-done", Note: "filler"}); err != nil {
				return err
			}
		}
		return nil
	})
	ctx.Step(`^the DRA registers one more wait$`, func() error {
		return c.run("cloche dra wait build-done --note extra")
	})
	ctx.Step(`^registration is refused as over the 20 pending wait limit$`, func() error {
		if c.exitCode == 0 || !strings.Contains(c.stderr, "20 pending wait limit") {
			return fmt.Errorf("exit %d, stderr %q", c.exitCode, c.stderr)
		}
		return nil
	})
	ctx.Step(`^the DRA registers a wait with interval "([^"]*)"$`, func(iv string) error {
		return c.run("cloche dra wait build-done --note x --interval " + iv)
	})
	ctx.Step(`^registration is refused as under the 1 minute minimum interval$`, func() error {
		if c.exitCode == 0 || !strings.Contains(c.stderr, "minimum interval") {
			return fmt.Errorf("exit %d, stderr %q", c.exitCode, c.stderr)
		}
		return nil
	})
	ctx.Step(`^a wait is registered without a timeout$`, func() error {
		// Make room first if the DRA is at the limit.
		if waits, _ := c.reg.List(c.dir, "qa"); len(waits) >= dra.MaxPendingWaits {
			if err := c.reg.Cancel(c.dir, "qa", waits[0].ID); err != nil {
				return err
			}
		}
		return c.register("build-done", "--note", "no timeout given")
	})
	ctx.Step(`^it is recorded with a 72 hour timeout$`, func() error {
		waits, err := c.reg.List(c.dir, "qa")
		if err != nil {
			return err
		}
		for _, w := range waits {
			if w.ID == c.lastID {
				if w.Timeout != 72*time.Hour {
					return fmt.Errorf("timeout %v", w.Timeout)
				}
				return nil
			}
		}
		return fmt.Errorf("wait %s not found", c.lastID)
	})
	ctx.Step(`^the DRA has registered a wait on "([^"]*)" and its wake-up has ended$`, func(poll string) error {
		if err := c.register(poll, "--note", "survives a restart"); err != nil {
			return err
		}
		c.drain() // the wake-up that registered it took its inbox and ended
		return nil
	})
	ctx.Step(`^the daemon restarts$`, func() error {
		// Everything in memory is gone; only the project's files remain.
		c.newRegistry()
		if waits, err := c.reg.List(c.dir, "qa"); err != nil || len(waits) != 1 {
			return fmt.Errorf("after the restart want 1 pending wait, got %+v (err %v)", waits, err)
		}
		return nil
	})
	ctx.Step(`^the poll later reports ready$`, func() error {
		c.runner.Script("build-done", dra.PollOutcome{Result: "green"})
		c.tick()
		return nil
	})
	ctx.Step(`^the DRA receives the wait event in its next wake-up message$`, func() error {
		c.drain()
		if len(c.waitEvents()) != 1 {
			return fmt.Errorf("want one wait event, got %+v", c.taken)
		}
		return nil
	})
	ctx.Step(`^the command is rejected as an unknown subcommand$`, func() error {
		if c.exitCode == 0 || !strings.Contains(c.stderr, "usage: cloche dra") {
			return fmt.Errorf("want rejection with usage; exit %d, stderr %q", c.exitCode, c.stderr)
		}
		return nil
	})
	ctx.Step(`^"([^"]*)" is not an event kind the inbox can deliver$`, func(kind string) error {
		// No code names the kind: the constant is gone from the trigger
		// sources and the daemon refuses it as a notification kind.
		for _, f := range []string{"../internal/dra/triggers.go", "../internal/adapters/grpc/dra_triggers.go"} {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), `"`+kind+`"`) || strings.Contains(string(b), "Kind"+strings.Title(kind)) {
				return fmt.Errorf("%s still names the %q event kind", f, kind)
			}
		}
		return nil
	})
}
