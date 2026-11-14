package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/adapters/web"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/module/qaconformance"
	"github.com/swordsmanluke/cloche/internal/signal"
)

func init() { registerScenarios(initQuestionsCLIScenarios) }

// These steps drive the real cloche binary against fakeQADaemon, an in-test
// stand-in for the daemon's questions routes (the daemon path itself is covered
// by the web handler tests and the qaconformance suite).
func initQuestionsCLIScenarios(ctx *godog.ScenarioContext) {
	q := &questionsCLI{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*q = questionsCLI{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		q.teardown()
		return c, nil
	})
	q.register(ctx)
	q.registerL2(ctx)
}

const (
	qcliAsking = "How to ask well here.\n"
	qcliJudge  = "How to judge here.\n"
)

var (
	qcliBuildOnce sync.Once
	qcliBinary    string
	qcliBuildErr  error
)

func questionsBinary() (string, error) {
	qcliBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cloche-bin-")
		if err != nil {
			qcliBuildErr = err
			return
		}
		qcliBinary = filepath.Join(dir, "cloche")
		out, err := exec.Command("go", "build", "-o", qcliBinary, "github.com/swordsmanluke/cloche/cmd/cloche").CombinedOutput()
		if err != nil {
			qcliBuildErr = fmt.Errorf("build cloche: %v\n%s", err, out)
		}
	})
	return qcliBinary, qcliBuildErr
}

type questionsCLI struct {
	dir      string
	srv      *httptest.Server
	daemon   *fakeQADaemon
	stdout   string
	stderr   string
	exitCode int
	extraEnv []string // extra env for the CLI process
	// L2: the stub backend answered through its own tool.
	stub       *qaconformance.StubBackend
	stubID     string
	stubRead   string
	suiteFails map[string]error
	// L2: the real web handler for the token scenario.
	handlerURL, handlerToken string
	responses                []int
}

type qcliQuestion struct {
	ID          string
	Prompt      string
	HumanAnswer string
}

// fakeQADaemon serves the questions routes in memory, checking the project's
// signal token like the real daemon.
type fakeQADaemon struct {
	mu        sync.Mutex
	token     string
	bound     bool
	asking    string
	judge     string
	questions []qcliQuestion
}

func (d *fakeQADaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	if r.Header.Get("Authorization") != "Bearer "+d.token {
		reply(http.StatusUnauthorized, map[string]string{"error": "missing or invalid bearer token"})
		return
	}
	if !d.bound {
		reply(http.StatusConflict, map[string]string{"error": host.ErrNoQABind.Error()})
		return
	}
	find := func(id string) *qcliQuestion {
		for i := range d.questions {
			if d.questions[i].ID == id {
				return &d.questions[i]
			}
		}
		return nil
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/questions/guide"):
		text := d.asking
		if r.URL.Query().Get("judge") != "" {
			text = d.judge
		}
		w.Write([]byte(text))
	case strings.HasSuffix(r.URL.Path, "/questions/ask"):
		var req struct {
			File struct {
				Questions []struct{ Prompt string } `json:"questions"`
			} `json:"file"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		ids := []string{}
		for _, q := range req.File.Questions {
			id := "Q" + strconv.Itoa(len(d.questions)+1)
			d.questions = append(d.questions, qcliQuestion{ID: id, Prompt: q.Prompt})
			ids = append(ids, id)
		}
		reply(http.StatusOK, map[string]any{"ids": ids})
	case strings.HasSuffix(r.URL.Path, "/questions/await"):
		var req struct{ IDs []string }
		json.NewDecoder(r.Body).Decode(&req)
		open := []string{}
		for _, id := range req.IDs {
			if q := find(id); q != nil && q.HumanAnswer == "" {
				open = append(open, id)
			}
		}
		reply(http.StatusOK, map[string]any{"open": open})
	case strings.HasSuffix(r.URL.Path, "/questions/read"):
		var req struct{ ID string }
		json.NewDecoder(r.Body).Decode(&req)
		if q := find(req.ID); q != nil && q.HumanAnswer != "" {
			reply(http.StatusOK, map[string]string{"answer": q.HumanAnswer})
			return
		}
		reply(http.StatusNotFound, map[string]string{"error": "no answer"})
	default:
		reply(http.StatusNotFound, map[string]string{"error": "no such route"})
	}
}

func (q *questionsCLI) setup(bind bool) error {
	q.teardown()
	dir, err := os.MkdirTemp("", "cloche-questions-")
	if err != nil {
		return err
	}
	q.dir = dir
	q.daemon = &fakeQADaemon{token: "clo_sig_fake", bound: bind, asking: qcliAsking, judge: qcliJudge}
	q.srv = httptest.NewServer(q.daemon)
	// The CLI reads the project's token from $HOME/.config/cloche/signal-tokens.
	tokDir := filepath.Join(dir, "home", ".config", "cloche", "signal-tokens")
	store := signal.NewTokenStore(tokDir)
	real, err := store.Get(dir)
	if err != nil {
		return err
	}
	q.daemon.token = real
	return nil
}

func (q *questionsCLI) teardown() {
	if q.srv != nil {
		q.srv.Close()
		q.srv = nil
	}
	if q.stub != nil {
		q.stub.Close()
	}
	if q.dir != "" {
		os.RemoveAll(q.dir)
		q.dir = ""
	}
}

func (q *questionsCLI) writeFile(body string) error {
	return os.WriteFile(filepath.Join(q.dir, "questions.yaml"), []byte(body), 0o644)
}

func (q *questionsCLI) seed(qs ...qcliQuestion) error {
	q.daemon.questions = qs
	var ids []string
	for _, x := range qs {
		ids = append(ids, x.ID)
	}
	if err := os.MkdirAll(filepath.Join(q.dir, ".cloche"), 0o755); err != nil {
		return err
	}
	// As if `ask` had returned these ids earlier in the run.
	return os.WriteFile(filepath.Join(q.dir, ".cloche", "questions.ids"), []byte(strings.Join(ids, "\n")+"\n"), 0o644)
}

func (q *questionsCLI) run(args ...string) error {
	bin, err := questionsBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, append([]string{"questions"}, args...)...)
	cmd.Dir = q.dir
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(q.dir, "home"), "CLOCHE_HTTP="+q.srv.URL)
	cmd.Env = append(cmd.Env, q.extraEnv...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	q.exitCode = 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			return err
		}
		q.exitCode = ee.ExitCode()
	}
	q.stdout, q.stderr = o.String(), e.String()
	return nil
}

func (q *questionsCLI) register(ctx *godog.ScenarioContext) {
	ctx.Step(`^a project with a stub backend module bound to qa$`, func() error { return q.setup(true) })
	ctx.Step(`^a project with no qa backend bound$`, func() error { return q.setup(false) })
	ctx.Step(`^a question file with a title, context and two questions$`, func() error {
		return q.writeFile("title: T\ncontext: C\nquestions:\n  - prompt: one\n  - prompt: two\n")
	})
	ctx.Step(`^a question file with a question that has no prompt$`, func() error {
		return q.writeFile("title: T\ncontext: C\nquestions:\n  - detail: no prompt here\n")
	})
	ctx.Step(`^I run "cloche questions ask --file questions\.yaml"$`, func() error {
		return q.run("ask", "--file", "questions.yaml")
	})
	ctx.Step(`^I run "cloche questions ask --file questions\.yaml --no-judge"$`, func() error {
		return q.run("ask", "--file", "questions.yaml", "--no-judge")
	})
	ctx.Step(`^I run "cloche questions await"$`, func() error { return q.run("await") })
	ctx.Step(`^I run "cloche questions guide"$`, func() error { return q.run("guide") })
	ctx.Step(`^I run "cloche questions guide --judge"$`, func() error { return q.run("guide", "--judge") })
	ctx.Step(`^I run "cloche questions read" for the answered question$`, func() error { return q.run("read", "Q1") })
	ctx.Step(`^I run "cloche questions read" for the unanswered question$`, func() error { return q.run("read", "Q2") })
	ctx.Step(`^questions have been asked and none answered$`, func() error {
		return q.seed(qcliQuestion{ID: "Q1", Prompt: "one"}, qcliQuestion{ID: "Q2", Prompt: "two"})
	})
	ctx.Step(`^questions have been asked and all answered$`, func() error {
		return q.seed(qcliQuestion{ID: "Q1", Prompt: "one", HumanAnswer: "a"}, qcliQuestion{ID: "Q2", Prompt: "two", HumanAnswer: "b"})
	})
	ctx.Step(`^a question that has been answered with "use postgres"$`, func() error {
		return q.seed(qcliQuestion{ID: "Q1", Prompt: "db?", HumanAnswer: "use postgres"}, qcliQuestion{ID: "Q2", Prompt: "other"})
	})
	ctx.Step(`^a question that has not been answered$`, func() error { return nil })             // Q2, seeded above
	ctx.Step(`^the backend module ships asking\.md and judge\.md$`, func() error { return nil }) // written by setup
	ctx.Step(`^the command exits 0$`, func() error { return q.expectExit(0) })
	ctx.Step(`^the command exits 1$`, func() error { return q.expectExit(1) })
	ctx.Step(`^the command exits non-zero$`, func() error {
		if q.exitCode == 0 {
			return fmt.Errorf("expected non-zero exit; stdout %q", q.stdout)
		}
		return nil
	})
	ctx.Step(`^the error names the missing prompt$`, func() error { return q.stderrHas("missing a prompt") })
	ctx.Step(`^the error mentions "bind qa = <module>"$`, func() error { return q.stderrHas("bind qa = <module>") })
	ctx.Step(`^no questions are created$`, func() error {
		if n := len(q.daemon.questions); n != 0 {
			return fmt.Errorf("%d questions created", n)
		}
		return nil
	})
	ctx.Step(`^the output is "CLOCHE_RESULT:answered"$`, func() error { return q.outputIs("CLOCHE_RESULT:answered") })
	ctx.Step(`^the output is "use postgres"$`, func() error { return q.outputIs("use postgres") })
	ctx.Step(`^the output is empty$`, func() error { return q.outputIs("") })
	ctx.Step(`^the output is the contents of asking\.md$`, func() error { return q.outputIs(strings.TrimRight(qcliAsking, "\n")) })
	ctx.Step(`^the output is the contents of judge\.md$`, func() error { return q.outputIs(strings.TrimRight(qcliJudge, "\n")) })
	ctx.Step(`^the output lists one new question id per question$`, func() error {
		return q.outputIs("Q1\nQ2")
	})
}

func (q *questionsCLI) expectExit(want int) error {
	if q.exitCode != want {
		return fmt.Errorf("exit code %d, want %d (stdout %q stderr %q)", q.exitCode, want, q.stdout, q.stderr)
	}
	return nil
}

func (q *questionsCLI) stderrHas(sub string) error {
	if !strings.Contains(q.stderr, sub) {
		return fmt.Errorf("stderr %q does not contain %q", q.stderr, sub)
	}
	return nil
}

func (q *questionsCLI) outputIs(want string) error {
	if got := strings.TrimRight(q.stdout, "\n"); got != want {
		return fmt.Errorf("output %q, want %q", got, want)
	}
	return nil
}

// qaTaskProvider is the daemon side of the questions routes for the token
// scenario: it records the slot instead of running a module (module execution
// is covered by the host tests and the conformance suite).
type qaTaskProvider struct {
	web.TaskProvider // unused methods; nil
	slots            []string
}

func (p *qaTaskProvider) RunTrackerPhase(_ context.Context, _ string, slot string, _ map[string]string) (map[string]string, error) {
	p.slots = append(p.slots, slot)
	return map[string]string{"question_ids": "Q1"}, nil
}

func (q *questionsCLI) registerL2(ctx *godog.ScenarioContext) {
	var provider *qaTaskProvider
	var handler http.Handler
	var token string

	ctx.Step(`^the daemon is running with the stub backend bound to qa$`, func() error {
		store, err := sqlite.NewStore(":memory:")
		if err != nil {
			return err
		}
		run := domain.NewRun("run-1", "develop")
		run.ProjectDir = q.dir
		if err := store.CreateRun(context.Background(), run); err != nil {
			return err
		}
		tokens := signal.NewTokenStore(filepath.Join(q.dir, "daemon-tokens"))
		if token, err = tokens.Get(q.dir); err != nil {
			return err
		}
		provider = &qaTaskProvider{}
		h, err := web.NewHandler(store, store, web.WithSignalIntake(tokens, nil), web.WithTaskProvider(provider))
		if err != nil {
			return err
		}
		handler = h
		return nil
	})
	post := func(route, body, auth string) int {
		method := "POST"
		if route == "guide" {
			method = "GET"
		}
		req := httptest.NewRequest(method, "/api/projects/"+url.PathEscape(q.dir)+"/questions/"+route, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	routes := map[string]string{"ask": `{"file":{"title":"T"}}`, "await": `{"ids":["Q1"]}`, "read": `{"id":"Q1"}`}
	ctx.Step(`^I POST to the questions ask, await and read routes with no token$`, func() error {
		q.responses = nil
		for r, b := range routes {
			q.responses = append(q.responses, post(r, b, ""))
		}
		return nil
	})
	ctx.Step(`^I POST to them with an invalid token$`, func() error {
		q.responses = nil
		for r, b := range routes {
			q.responses = append(q.responses, post(r, b, "Bearer nope"))
		}
		return nil
	})
	ctx.Step(`^each response is rejected as unauthorized$`, func() error {
		for _, c := range q.responses {
			if c != http.StatusUnauthorized {
				return fmt.Errorf("status %d, want 401 (all: %v)", c, q.responses)
			}
		}
		if len(provider.slots) != 0 {
			return fmt.Errorf("slots ran without a valid token: %v", provider.slots)
		}
		return nil
	})
	ctx.Step(`^I POST to the ask route with the project's signal token$`, func() error {
		q.responses = []int{post("ask", routes["ask"], "Bearer "+token)}
		return nil
	})
	ctx.Step(`^the response succeeds and returns question ids$`, func() error {
		if len(q.responses) != 1 || q.responses[0] != http.StatusOK || len(provider.slots) != 1 || provider.slots[0] != host.SlotAsk {
			return fmt.Errorf("responses %v, slots %v", q.responses, provider.slots)
		}
		return nil
	})

	// Human beside judge, through the stub backend module's own tool.
	askStub := func() error {
		if q.stub != nil {
			return nil
		}
		b, err := qaconformance.NewStubBackend()
		if err != nil {
			return err
		}
		q.stub = b
		f := filepath.Join(q.dir, "q.json")
		if err := os.WriteFile(f, []byte(qaconformance.QuestionsFileBody), 0o644); err != nil {
			return err
		}
		ids, err := b.Ask(f)
		if err != nil {
			return err
		}
		q.stubID = ids[0]
		return nil
	}
	ctx.Step(`^a question answered by a judge with "([^"]*)"$`, func(text string) error {
		if err := askStub(); err != nil {
			return err
		}
		return q.stub.Answer(q.stubID, "judge", text)
	})
	ctx.Step(`^the same question answered by a human with "([^"]*)"$`, func(text string) error {
		return q.stub.Answer(q.stubID, "human", text)
	})
	ctx.Step(`^I run "cloche questions read" for that question$`, func() error {
		// The read-answer slot of the bound stub module (the daemon returns its answer_path text).
		var err error
		q.stubRead, err = q.stub.Read(q.stubID)
		return err
	})
	ctx.Step(`^the output is "human says B"$`, func() error {
		if got := strings.TrimSpace(q.stubRead); got != "human says B" {
			return fmt.Errorf("output %q", got)
		}
		return nil
	})

	ctx.Step(`^the conformance suite and a stub tool backing a backend module$`, func() error { return nil })
	ctx.Step(`^the suite runs ask, open, answer, none-open, read and human-beside-judge$`, func() error {
		q.suiteFails = qaconformance.RunAll(func() (qaconformance.Backend, error) {
			b, err := qaconformance.NewStubBackend()
			return b, err
		})
		return nil
	})
	ctx.Step(`^every conformance check passes$`, func() error {
		if len(q.suiteFails) > 0 {
			return fmt.Errorf("conformance failures: %v", q.suiteFails)
		}
		return nil
	})
}
