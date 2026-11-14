package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func qaProject(t *testing.T) string { return t.TempDir() }

// fakeQuestionsClient is a test-only in-memory questionsClient. Human answers
// beat judge answers, as the real backends' read-answer slot guarantees.
type fakeQuestionsClient struct {
	Questions []*fakeQuestion
	unbound   bool
	noJudge   bool
}

type fakeQuestion struct{ ID, HumanAnswer, JudgeAnswer string }

func (q *fakeQuestion) answer() (string, bool) {
	if q.HumanAnswer != "" {
		return q.HumanAnswer, true
	}
	return q.JudgeAnswer, q.JudgeAnswer != ""
}

func (f *fakeQuestionsClient) Ask(qf *QuestionFile, noJudge bool) ([]string, error) {
	f.noJudge = noJudge
	if f.unbound {
		return nil, errors.New("no qa backend is bound; add `bind qa = <module>`")
	}
	var ids []string
	for range qf.Questions {
		id := "Q" + strconv.Itoa(len(f.Questions)+1)
		f.Questions = append(f.Questions, &fakeQuestion{ID: id})
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *fakeQuestionsClient) Open(ids []string) ([]string, error) {
	var open []string
	for _, q := range f.Questions {
		for _, id := range ids {
			if _, ok := q.answer(); id == q.ID && !ok {
				open = append(open, id)
			}
		}
	}
	return open, nil
}

func (f *fakeQuestionsClient) Read(id string) (string, bool, error) {
	for _, q := range f.Questions {
		if q.ID == id {
			a, ok := q.answer()
			return a, ok, nil
		}
	}
	return "", false, nil
}

func (f *fakeQuestionsClient) Guide(judge bool) (string, error) {
	if judge {
		return "judge well\n", nil
	}
	return "ask well\n", nil
}

func writeQ(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "q.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoQuestions = "title: T\ncontext: C\nquestions:\n  - prompt: one\n  - prompt: two\n    options:\n      - {label: a, recommended: true}\nbackend: {feature: x}\n"

func withFake(t *testing.T) *fakeQuestionsClient {
	f := &fakeQuestionsClient{}
	old := newQuestionsClient
	newQuestionsClient = func(string) (questionsClient, error) { return f, nil }
	t.Cleanup(func() { newQuestionsClient = old })
	return f
}

func run(t *testing.T, args ...string) (string, error) {
	var buf bytes.Buffer
	err := questionsCommand(args, &buf)
	return buf.String(), err
}

func TestParseQuestionFile(t *testing.T) {
	if _, err := parseQuestionFile([]byte(twoQuestions)); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"title: T\nquestions:\n  - detail: x\n":                                         "question 1 is missing a prompt",
		"questions:\n  - prompt: p\n":                                                   "missing title",
		"title: T\nquestions: []\n":                                                     "no questions",
		"title: T\nbogus: 1\nquestions:\n  - prompt: p\n":                               "bogus",
		"title: T\nquestions:\n  - prompt: p\n    options:\n      - {description: d}\n": "missing a label",
		"": "empty",
	}
	for in, want := range cases {
		_, err := parseQuestionFile([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
}

func TestQuestionsAskAwaitRead(t *testing.T) {
	f := withFake(t)
	dir := qaProject(t)
	out, err := run(t, "ask", "--file", writeQ(t, twoQuestions), "--project", dir)
	if err != nil || out != "Q1\nQ2\n" {
		t.Fatalf("ask: %q %v", out, err)
	}
	if out, _ := run(t, "await", "--project", dir); out != "" {
		t.Fatalf("await with open questions printed %q", out)
	}
	f.Questions[0].JudgeAnswer = "judge"
	f.Questions[1].HumanAnswer = "use postgres"
	if out, _ := run(t, "await", "Q1", "Q2", "--project", dir); out != "CLOCHE_RESULT:answered\n" {
		t.Fatalf("await: %q", out)
	}
	if out, err := run(t, "read", "Q2", "--project", dir); err != nil || out != "use postgres\n" {
		t.Fatalf("read: %q %v", out, err)
	}
}

func TestQuestionsReadNoAnswerExits1(t *testing.T) {
	withFake(t)
	dir := qaProject(t)
	if _, err := run(t, "ask", "--file", writeQ(t, twoQuestions), "--project", dir); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "read", "Q1", "--project", dir)
	var ee questionsExitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("got %v", err)
	}
}

func TestQuestionsAskUnboundNamesBind(t *testing.T) {
	f := withFake(t)
	f.unbound = true
	_, err := run(t, "ask", "--file", writeQ(t, twoQuestions), "--project", qaProject(t))
	if err == nil || !strings.Contains(err.Error(), "bind qa = <module>") {
		t.Fatalf("got %v", err)
	}
	if len(f.Questions) != 0 {
		t.Fatal("questions created")
	}
}

func TestQuestionsAskMalformedCreatesNothing(t *testing.T) {
	f := withFake(t)
	_, err := run(t, "ask", "--file", writeQ(t, "title: T\nquestions:\n  - detail: x\n"), "--project", qaProject(t))
	if err == nil || !strings.Contains(err.Error(), "missing a prompt") {
		t.Fatalf("got %v", err)
	}
	if len(f.Questions) != 0 {
		t.Fatal("questions created")
	}
}

func TestQuestionsNoJudgeIsSentToDaemon(t *testing.T) {
	f := withFake(t)
	out, err := run(t, "ask", "--file", writeQ(t, twoQuestions), "--no-judge", "--project", qaProject(t))
	if err != nil || out != "Q1\nQ2\n" {
		t.Fatalf("%q %v", out, err)
	}
	if !f.noJudge {
		t.Fatal("--no-judge was not passed to the daemon")
	}
	f.noJudge = false
	if _, err := run(t, "ask", "--file", writeQ(t, twoQuestions), "--project", qaProject(t)); err != nil || f.noJudge {
		t.Fatalf("plain ask must not set no_judge: %v", err)
	}
}

func TestQuestionsGuide(t *testing.T) {
	withFake(t)
	dir := qaProject(t)
	if out, err := run(t, "guide", "--project", dir); err != nil || out != "ask well\n" {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := run(t, "guide", "--judge", "--project", dir); err != nil || out != "judge well\n" {
		t.Fatalf("%q %v", out, err)
	}
}

// The real client sends the bearer token, maps 404 on read to "no answer",
// and surfaces the daemon's error text.
func TestDaemonQuestionsClient(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path+"?"+r.URL.RawQuery
		switch {
		case strings.HasSuffix(r.URL.Path, "/ask"):
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"no qa backend is bound; add bind qa = <module>"}`))
		case strings.HasSuffix(r.URL.Path, "/read"):
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"no answer"}`))
		case strings.HasSuffix(r.URL.Path, "/await"):
			w.Write([]byte(`{"open":["Q2"]}`))
		default:
			w.Write([]byte("guide text"))
		}
	}))
	defer srv.Close()
	c := &daemonQuestionsClient{base: srv.URL + "/api/projects/p/questions", token: "tok"}

	if _, err := c.Ask(&QuestionFile{Title: "T"}, false); err == nil || !strings.Contains(err.Error(), "bind qa") {
		t.Fatalf("ask error = %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if _, ok, err := c.Read("Q1"); ok || err != nil {
		t.Fatalf("read of unanswered: ok=%v err=%v", ok, err)
	}
	if open, err := c.Open([]string{"Q1", "Q2"}); err != nil || len(open) != 1 || open[0] != "Q2" {
		t.Fatalf("open = %v %v", open, err)
	}
	if g, err := c.Guide(true); err != nil || g != "guide text" || !strings.HasSuffix(gotPath, "/guide?judge=1") {
		t.Fatalf("guide = %q %v (%s)", g, err, gotPath)
	}
}

// A bare await checks the ids recorded by ask.
func TestQuestionsAwaitUsesAskedIDs(t *testing.T) {
	f := withFake(t)
	dir := qaProject(t)
	if _, err := run(t, "await", "--project", dir); err == nil {
		t.Fatal("await with no ids and no ask should fail")
	}
	if _, err := run(t, "ask", "--file", writeQ(t, twoQuestions), "--project", dir); err != nil {
		t.Fatal(err)
	}
	f.Questions[0].HumanAnswer = "a"
	f.Questions[1].HumanAnswer = "b"
	if out, _ := run(t, "await", "--project", dir); out != "CLOCHE_RESULT:answered\n" {
		t.Fatalf("await: %q", out)
	}
}

func TestQuestionsCompletionListed(t *testing.T) {
	found := false
	for _, c := range completionSubcommands {
		found = found || c == "questions"
	}
	if !found {
		t.Fatal("questions missing from completionSubcommands")
	}
	if !strings.Contains(subcommandHelp["questions"], "cloche questions ask") {
		t.Fatal("help missing")
	}
}
