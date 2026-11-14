package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// questionsAnsweredMarker is the poll-protocol result `await` prints once no
// question is open.
const questionsAnsweredMarker = "CLOCHE_RESULT:answered"

// QuestionFile is the file `cloche questions ask --file` reads (JSON or YAML).
// Backend is opaque: cloche passes it through to the bound module.
type QuestionFile struct {
	Title     string         `yaml:"title" json:"title"`
	Context   string         `yaml:"context" json:"context"`
	Questions []QuestionSpec `yaml:"questions" json:"questions"`
	Backend   map[string]any `yaml:"backend" json:"backend,omitempty"`
}

// QuestionSpec is one question in a QuestionFile.
type QuestionSpec struct {
	Prompt  string           `yaml:"prompt" json:"prompt"`
	Detail  string           `yaml:"detail" json:"detail,omitempty"`
	Options []QuestionOption `yaml:"options" json:"options,omitempty"`
}

// QuestionOption is a suggested answer.
type QuestionOption struct {
	Label       string `yaml:"label" json:"label"`
	Description string `yaml:"description" json:"description,omitempty"`
	Recommended bool   `yaml:"recommended" json:"recommended,omitempty"`
}

// parseQuestionFile parses and validates a question file.
func parseQuestionFile(data []byte) (*QuestionFile, error) {
	var qf QuestionFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&qf); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("question file is empty")
		}
		return nil, fmt.Errorf("parse question file: %w", err)
	}
	if strings.TrimSpace(qf.Title) == "" {
		return nil, fmt.Errorf("question file: missing title")
	}
	if len(qf.Questions) == 0 {
		return nil, fmt.Errorf("question file: no questions")
	}
	for i, q := range qf.Questions {
		n := i + 1
		if strings.TrimSpace(q.Prompt) == "" {
			return nil, fmt.Errorf("question file: question %d is missing a prompt", n)
		}
		for j, o := range q.Options {
			if strings.TrimSpace(o.Label) == "" {
				return nil, fmt.Errorf("question file: question %d option %d is missing a label", n, j+1)
			}
		}
	}
	return &qf, nil
}

// questionsClient is what the CLI needs from the daemon: the routes
// POST /api/projects/{name}/questions/ask|await|read and GET .../guide.
type questionsClient interface {
	// Ask posts the question file and returns the new ids in order.
	Ask(qf *QuestionFile) ([]string, error)
	// Open returns the subset of ids that have no answer.
	Open(ids []string) ([]string, error)
	// Read returns the answer for id; ok is false when there is none.
	Read(id string) (answer string, ok bool, err error)
	// Guide returns the bound module's asking.md (or judge.md).
	Guide(judge bool) (string, error)
}

// newQuestionsClient builds the daemon client for a project. Tests replace it.
var newQuestionsClient = func(projectDir string) (questionsClient, error) {
	// A DRA reaches the same routes through its own API, with its credential.
	if c := draModeClient(); c != nil {
		return &daemonQuestionsClient{base: c.base + "/api/dra/questions", token: c.token}, nil
	}
	httpAddr := resolveHTTPAddr()
	if httpAddr == "" {
		httpAddr = "localhost:8080"
	}
	token, err := signal.NewTokenStore(config.SignalTokenDir()).Get(projectDir)
	if err != nil {
		return nil, fmt.Errorf("reading the project's signal token: %w", err)
	}
	return &daemonQuestionsClient{base: fmt.Sprintf("http://%s/api/projects/%s/questions", httpAddr, url.PathEscape(projectDir)), token: token}, nil
}

// daemonQuestionsClient talks to the daemon's questions routes with the
// project's signal token.
type daemonQuestionsClient struct{ base, token string }

// errNoAnswer marks a 404 from the read route.
var errNoAnswer = errors.New("no answer")

func (c *daemonQuestionsClient) do(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd) //nolint:noctx
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon web API: %w\nhint: set CLOCHE_HTTP (or [daemon] http in ~/.config/cloche/config) and restart the daemon", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading daemon response: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		return data, nil
	}
	var e struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	if resp.StatusCode == http.StatusNotFound && path == "/read" {
		return nil, errNoAnswer
	}
	return nil, errors.New(msg)
}

func (c *daemonQuestionsClient) Ask(qf *QuestionFile) ([]string, error) {
	data, err := c.do("POST", "/ask", map[string]any{"file": qf})
	if err != nil {
		return nil, err
	}
	var r struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return r.IDs, nil
}

func (c *daemonQuestionsClient) Open(ids []string) ([]string, error) {
	data, err := c.do("POST", "/await", map[string]any{"ids": ids})
	if err != nil {
		return nil, err
	}
	var r struct {
		Open []string `json:"open"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return r.Open, nil
}

func (c *daemonQuestionsClient) Read(id string) (string, bool, error) {
	data, err := c.do("POST", "/read", map[string]string{"id": id})
	if errors.Is(err, errNoAnswer) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var r struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", false, fmt.Errorf("parsing response: %w", err)
	}
	return r.Answer, true, nil
}

func (c *daemonQuestionsClient) Guide(judge bool) (string, error) {
	path := "/guide"
	if judge {
		path += "?judge=1"
	}
	data, err := c.do("GET", path, nil)
	return string(data), err
}

// askedIDsFile records the ids `ask` returned so a bare `await` can check
// them. It lives in the project so it is shared by every step of a run.
func askedIDsFile(projectDir string) string {
	return filepath.Join(projectDir, ".cloche", "questions.ids")
}

func recordAskedIDs(projectDir string, ids []string) error {
	if err := os.MkdirAll(filepath.Dir(askedIDsFile(projectDir)), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(askedIDsFile(projectDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, strings.Join(ids, "\n"))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func loadAskedIDs(projectDir string) ([]string, error) {
	data, err := os.ReadFile(askedIDsFile(projectDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading asked question ids: %w", err)
	}
	return strings.Fields(string(data)), nil
}

// questionsExitError makes the command exit with a code without an error line.
type questionsExitError struct{ code int }

func (e questionsExitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func cmdQuestions(args []string) {
	err := questionsCommand(args, os.Stdout)
	var ee questionsExitError
	if errors.As(err, &ee) {
		os.Exit(ee.code)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func questionsCommand(args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cloche questions ask|await|read|guide (see 'cloche help questions')")
	}
	sub, rest := args[0], args[1:]
	var file, project string
	var noJudge, judge bool
	var pos []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		val := func() (string, error) {
			if i+1 >= len(rest) {
				return "", fmt.Errorf("%s requires a value", a)
			}
			i++
			return rest[i], nil
		}
		var err error
		switch {
		case a == "--file" && sub == "ask":
			file, err = val()
		case a == "--no-judge" && sub == "ask":
			noJudge = true
		case a == "--judge" && sub == "guide":
			judge = true
		case a == "--project" || a == "-p":
			project, err = val()
		case strings.HasPrefix(a, "-"):
			err = fmt.Errorf("unknown flag for %s: %s", sub, a)
		default:
			pos = append(pos, a)
		}
		if err != nil {
			return err
		}
	}
	_ = noJudge // TODO(L3): gate the judge run; accepted and ignored for now.
	dir := resolveProjectDir(project)

	switch sub {
	case "ask":
		if file == "" || len(pos) > 0 {
			return fmt.Errorf("usage: cloche questions ask --file <path> [--no-judge] [--project <dir>]")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read question file: %w", err)
		}
		qf, err := parseQuestionFile(data)
		if err != nil {
			return err
		}
		c, err := newQuestionsClient(dir)
		if err != nil {
			return err
		}
		ids, err := c.Ask(qf)
		if err != nil {
			return err
		}
		// A DRA's project is read-only and it passes ids explicitly (to
		// `await` or `dra wait questions`), so it has no use for the record.
		if draModeClient() == nil {
			if err := recordAskedIDs(dir, ids); err != nil {
				return fmt.Errorf("recording asked question ids: %w", err)
			}
		}
		for _, id := range ids {
			fmt.Fprintln(w, id)
		}
		return nil
	case "await":
		c, err := newQuestionsClient(dir)
		if err != nil {
			return err
		}
		ids := pos
		if len(ids) == 0 && draModeClient() == nil {
			if ids, err = loadAskedIDs(dir); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return fmt.Errorf("no question ids: pass them, or run 'cloche questions ask' first")
		}
		open, err := c.Open(ids)
		if err != nil {
			return err
		}
		if len(open) == 0 {
			fmt.Fprintln(w, questionsAnsweredMarker)
		}
		return nil
	case "read":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cloche questions read <id> [--project <dir>]")
		}
		c, err := newQuestionsClient(dir)
		if err != nil {
			return err
		}
		ans, ok, err := c.Read(pos[0])
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "question %s has no answer yet\n", pos[0])
			return questionsExitError{1}
		}
		fmt.Fprintln(w, strings.TrimRight(ans, "\n"))
		return nil
	case "guide":
		if len(pos) > 0 {
			return fmt.Errorf("usage: cloche questions guide [--judge] [--project <dir>]")
		}
		c, err := newQuestionsClient(dir)
		if err != nil {
			return err
		}
		text, err := c.Guide(judge)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, text)
		return err
	default:
		return fmt.Errorf("unknown questions subcommand: %s", sub)
	}
}
