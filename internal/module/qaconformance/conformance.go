// Package qaconformance is the shared conformance suite for qa backend
// modules (docs/modules.md §Questions). A module author points Run at a
// Backend that drives the module's ask / await-answer / read-answer workflows
// and a way to answer questions through the module's own tool.
package qaconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Backend is the module under test.
type Backend interface {
	// Ask runs the ask slot with kv questions_file = file and returns out question_ids.
	Ask(file string) (ids []string, err error)
	// Open runs await-answer with kv question_ids = ids and returns out
	// questions_open. It must not block.
	Open(ids []string) (open []string, err error)
	// Read runs read-answer with kv question_id = id and returns the text at
	// out answer_path. It returns an error when the question has no answer.
	Read(id string) (answer string, err error)
	// Answer records an answer through the backend's own tool, as a human
	// or as a judge (who is "human" or "judge").
	Answer(id, who, text string) error
}

// Check is one named conformance check run against a fresh Backend.
type Check struct {
	Name string
	Fn   func(b Backend, questionsFile string) error
}

// QuestionsFileBody is the question file every check asks with: two questions.
const QuestionsFileBody = `{"title":"T","context":"C","questions":[{"prompt":"one"},{"prompt":"two"}]}`

func equal(what string, got, want []string) error {
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("%s = %v, want %v", what, got, want)
	}
	return nil
}

// Checks lists the contract: ask, open, answer, none open, read, and a human
// answer returned beside a judge answer.
func Checks() []Check {
	ask := func(b Backend, f string) ([]string, error) {
		ids, err := b.Ask(f)
		if err != nil {
			return nil, fmt.Errorf("ask: %w", err)
		}
		if len(ids) != 2 {
			return nil, fmt.Errorf("ask returned %d ids %v, want one per question (2)", len(ids), ids)
		}
		return ids, nil
	}
	return []Check{
		{"ask", func(b Backend, f string) error { _, err := ask(b, f); return err }},
		{"open", func(b Backend, f string) error {
			ids, err := ask(b, f)
			if err != nil {
				return err
			}
			open, err := b.Open(ids)
			if err != nil {
				return err
			}
			return equal("open before any answer", open, ids)
		}},
		{"answer", func(b Backend, f string) error {
			ids, err := ask(b, f)
			if err != nil {
				return err
			}
			if err := b.Answer(ids[0], "human", "yes"); err != nil {
				return err
			}
			open, err := b.Open(ids)
			if err != nil {
				return err
			}
			return equal("open after answering the first", open, ids[1:])
		}},
		{"none-open", func(b Backend, f string) error {
			ids, err := ask(b, f)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := b.Answer(id, "human", "yes"); err != nil {
					return err
				}
			}
			open, err := b.Open(ids)
			if err != nil {
				return err
			}
			return equal("open after answering all", open, nil)
		}},
		{"read", func(b Backend, f string) error {
			ids, err := ask(b, f)
			if err != nil {
				return err
			}
			if _, err := b.Read(ids[0]); err == nil {
				return fmt.Errorf("read of an unanswered question must fail")
			}
			if err := b.Answer(ids[0], "human", "use postgres"); err != nil {
				return err
			}
			got, err := b.Read(ids[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(got) != "use postgres" {
				return fmt.Errorf("read = %q, want %q", got, "use postgres")
			}
			return nil
		}},
		{"human-beside-judge", func(b Backend, f string) error {
			ids, err := ask(b, f)
			if err != nil {
				return err
			}
			// Judge first and human first both: the human answer must win either way.
			for i, order := range [][]string{{"judge", "human"}, {"human", "judge"}} {
				for _, who := range order {
					if err := b.Answer(ids[i], who, who+" says"); err != nil {
						return err
					}
				}
				got, err := b.Read(ids[i])
				if err != nil {
					return err
				}
				if strings.TrimSpace(got) != "human says" {
					return fmt.Errorf("read with %v answers = %q, want the human's", order, got)
				}
			}
			return nil
		}},
	}
}

// RunAll runs every check against a fresh backend from newBackend and returns
// the failures keyed by check name. For callers without a *testing.T.
func RunAll(newBackend func() (Backend, error)) map[string]error {
	fails := map[string]error{}
	dir, err := os.MkdirTemp("", "qa-conformance-")
	if err != nil {
		return map[string]error{"setup": err}
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "questions.json")
	if err := os.WriteFile(file, []byte(QuestionsFileBody), 0o644); err != nil {
		return map[string]error{"setup": err}
	}
	for _, c := range Checks() {
		b, err := newBackend()
		if err == nil {
			err = c.Fn(b, file)
		}
		if cl, ok := b.(interface{ Close() }); ok {
			cl.Close()
		}
		if err != nil {
			fails[c.Name] = err
		}
	}
	return fails
}

// Run is RunAll as subtests.
func Run(t *testing.T, newBackend func(t *testing.T) Backend) {
	t.Helper()
	for _, c := range Checks() {
		t.Run(c.Name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "questions.json")
			if err := os.WriteFile(file, []byte(QuestionsFileBody), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := c.Fn(newBackend(t), file); err != nil {
				t.Fatal(err)
			}
		})
	}
}
