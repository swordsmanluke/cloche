package features_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initSagaRealShapeScenarios) }

// realShapeWorld reuses sagaWorld's module-script runner but feeds read.sh a
// fixed `saga show` JSON in the shape real Saga emits.
type realShapeWorld struct {
	sagaWorld
	humanText, agentText, laterText string
}

func (w *realShapeWorld) fixture(showJSON string) error {
	if err := w.installStub(); err != nil {
		return err
	}
	f := filepath.Join(w.dir, "show.json")
	if err := os.WriteFile(f, []byte(showJSON), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, "bin", "saga"), []byte("#!/usr/bin/env bash\ncat "+f+"\n"), 0o755)
}

// activeRealShape lets other features reuse this world's read.sh runner and
// its "the read exits ..." steps.
var activeRealShape *realShapeWorld

func initSagaRealShapeScenarios(ctx *godog.ScenarioContext) {
	w := &realShapeWorld{}
	activeRealShape = w
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = realShapeWorld{humanText: "the human ruling", agentText: "the agent guess", laterText: "the later agent ruling"}
		return c, w.setup()
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		os.RemoveAll(w.dir)
		return c, nil
	})

	// L1
	ctx.Step(`^a real-shaped saga question with one answer by author "([^"]*)" with select \[1\] and status null$`,
		func(author string) error {
			return w.fixture(`{"id":"Q1","status":{"state":"in-force"},"answers":[{"author":"` + author +
				`","select":[1],"status":null,"text":"` + w.humanText + `"}]}`)
		})
	ctx.Step(`^a real-shaped saga question answered by both "([^"]*)" and "([^"]*)"$`,
		func(a, b string) error {
			return w.fixture(`{"id":"Q1","answers":[{"author":"` + a + `","status":null,"text":"` + w.agentText +
				`"},{"author":"` + b + `","status":null,"text":"` + w.humanText + `"}]}`)
		})
	ctx.Step(`^a real-shaped saga question with an agent answer, a later agent answer and a deprecated agent answer after it$`,
		func() error {
			return w.fixture(`{"id":"Q1","answers":[{"author":"agent","status":null,"text":"` + w.agentText +
				`"},{"author":"agent","status":null,"text":"` + w.laterText +
				`"},{"author":"agent","status":{"state":"deprecated"},"text":"deprecated ruling"}]}`)
		})
	ctx.Step(`^a real-shaped saga question with no answers$`,
		func() error { return w.fixture(`{"id":"Q1","answers":[]}`) })
	ctx.Step(`^the saga module reads that question$`, func() error {
		_, err := w.phase("read.sh", map[string]string{"question_id": "Q1"})
		return err
	})
	ctx.Step(`^the read exits 0$`, func() error {
		if w.rc != nil {
			return fmt.Errorf("read.sh failed: %v: %s", w.rc, w.out)
		}
		return nil
	})
	ctx.Step(`^the read exits non-zero$`, func() error {
		if w.rc == nil {
			return errors.New("read.sh succeeded, want failure")
		}
		return nil
	})
	answerIs := func(want string) error {
		if w.rc != nil {
			return fmt.Errorf("read.sh failed: %v: %s", w.rc, w.out)
		}
		kv, err := os.ReadFile(filepath.Join(w.dir, "kv", "answer_path"))
		if err != nil {
			return err
		}
		got, err := os.ReadFile(string(kv))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(got)) != want {
			return fmt.Errorf("answer = %q, want %q", got, want)
		}
		return nil
	}
	ctx.Step(`^the read prints the human answer's text$`, func() error { return answerIs(w.humanText) })
	ctx.Step(`^the read does not print the agent answer's text$`, func() error {
		kv, err := os.ReadFile(filepath.Join(w.dir, "kv", "answer_path"))
		if err != nil {
			return err
		}
		got, err := os.ReadFile(string(kv))
		if err != nil {
			return err
		}
		if strings.Contains(string(got), w.agentText) {
			return fmt.Errorf("answer contains agent text: %q", got)
		}
		return nil
	})
	ctx.Step(`^the read prints the later non-deprecated agent answer's text$`, func() error { return answerIs(w.laterText) })
	ctx.Step(`^the read reports the question has no answer yet$`, func() error {
		if !strings.Contains(w.out, "no answer") {
			return fmt.Errorf("output %q lacks \"no answer\"", w.out)
		}
		return nil
	})

	// L2
	ctx.Step(`^I inspect the stub saga used by the module and feature tests$`, func() error {
		if err := w.installStub(); err != nil {
			return err
		}
		qf, err := w.writeQuestions("cloche/qa-backends")
		if err != nil {
			return err
		}
		if _, err := w.phase("ask.sh", map[string]string{"questions_file": qf}); err != nil {
			return err
		}
		if w.rc != nil {
			return fmt.Errorf("ask failed: %v: %s", w.rc, w.out)
		}
		for _, who := range []string{"human", "agent"} {
			if err := w.answerAs("Q1", who, "text from "+who); err != nil {
				return err
			}
		}
		cmd := exec.Command(filepath.Join(w.dir, "bin", "saga"), "show", "Q1", "--json")
		cmd.Env = w.env()
		out, err := cmd.Output()
		if err != nil {
			return err
		}
		w.out = string(out)
		return nil
	})
	ctx.Step(`^every stubbed answer has a string author and a null status$`, func() error {
		var show struct {
			Answers []map[string]any `json:"answers"`
		}
		if err := json.Unmarshal([]byte(w.out), &show); err != nil {
			return err
		}
		if len(show.Answers) != 2 {
			return fmt.Errorf("want 2 stubbed answers, got %s", w.out)
		}
		for _, a := range show.Answers {
			if _, ok := a["author"].(string); !ok {
				return fmt.Errorf("author is not a string: %v", a)
			}
			if v, ok := a["status"]; !ok || v != nil {
				return fmt.Errorf("status is not null: %v", a)
			}
		}
		return nil
	})
	var hits string
	ctx.Step(`^I search tracked files for the object-shaped author "kind" fixture$`, func() error {
		// Patterns are assembled so this file does not match itself.
		jqStyle := "author:{" + "kind"
		jsonStyle := `author":{"` + "kind"
		out, err := exec.Command("git", "grep", "-n", "-F", "-e", jqStyle, "-e", jsonStyle).CombinedOutput()
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 1 {
				return fmt.Errorf("git grep: %v: %s", err, out)
			}
			out = nil
		}
		hits = strings.TrimSpace(string(out))
		return nil
	})
	ctx.Step(`^no tracked file contains it$`, func() error {
		if hits != "" {
			return fmt.Errorf("object-shaped author still present:\n%s", hits)
		}
		return nil
	})
	ctx.Step(`^real-shaped saga questions that have all been answered$`, func() error {
		w.ids = []string{"Q1", "Q2"}
		return w.fixture(`{"id":"Q1","status":{"state":"in-force"},"answers":[` +
			`{"author":"human","select":[1],"status":null,"text":"ruling"}]}`)
	})
	ctx.Step(`^the saga module awaits those questions$`, func() error {
		return w.runAwait()
	})
	ctx.Step(`^the saga module reports no open questions$`, func() error {
		if len(w.open) != 0 {
			return fmt.Errorf("open = %v, want none", w.open)
		}
		return nil
	})
}
