package features_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initSagaReadSelectionScenarios) }

func initSagaReadSelectionScenarios(ctx *godog.ScenarioContext) {
	// L1 steps.
	var shapes map[string]string
	var unfixed map[string]error
	ctx.Step(`^I inspect the stub saga used by the saga module tests$`, func() error {
		shapes = selectionShapes()
		return nil
	})
	ctx.Step(`^it can emit selection-only, selection-plus-text, text-only and multi-select answers$`, func() error {
		for name, raw := range shapes {
			var show struct {
				Answers []map[string]any `json:"answers"`
				Options []map[string]any `json:"options"`
			}
			if err := json.Unmarshal([]byte(raw), &show); err != nil {
				return fmt.Errorf("%s: %v", name, err)
			}
			if len(show.Answers) != 1 || len(show.Options) == 0 {
				return fmt.Errorf("%s: want one answer and options: %s", name, raw)
			}
			a := show.Answers[0]
			_, hasText := a["text"]
			sel, _ := a["select"].([]any)
			switch name {
			case "selection-only":
				if hasText || len(sel) != 1 {
					return fmt.Errorf("%s: %s", name, raw)
				}
			case "selection+text":
				if !hasText || len(sel) != 1 {
					return fmt.Errorf("%s: %s", name, raw)
				}
			case "text-only":
				if !hasText || len(sel) != 0 {
					return fmt.Errorf("%s: %s", name, raw)
				}
			case "multi-select":
				if hasText || len(sel) != 2 {
					return fmt.Errorf("%s: %s", name, raw)
				}
			}
		}
		if len(shapes) != 4 {
			return fmt.Errorf("want 4 shapes, got %d", len(shapes))
		}
		return nil
	})
	ctx.Step(`^the new selection read tests run against the unfixed read\.sh$`, func() error {
		unfixed = map[string]error{}
		for name, raw := range selectionShapes() {
			want := map[string]string{
				"selection-only": "Selected: Yes", "selection+text": "Selected: Yes\nGo ahead",
				"text-only": "Maybe later", "multi-select": "Selected: A, B",
			}[name]
			unfixed[name] = readWithUnfixedScript(raw, want)
		}
		return nil
	})
	ctx.Step(`^the selection cases fail and the text-only case passes$`, func() error {
		for name, err := range unfixed {
			if name == "text-only" && err != nil {
				return fmt.Errorf("text-only should pass: %v", err)
			}
			if name != "text-only" && err == nil {
				return fmt.Errorf("%s should fail against the unfixed read.sh", name)
			}
		}
		return nil
	})

	// L2 steps: the real read.sh against a stub saga.
	var show string
	ctx.Step(`^a choice question with options "([^"]*)" and "([^"]*)" answered by selecting position (\d+) with no text$`, func(a, b string, pos int) error {
		show = selectionShow([]string{a, b}, []int{pos}, nil)
		return nil
	})
	ctx.Step(`^a choice question with options "([^"]*)" and "([^"]*)" answered by selecting position (\d+) with text "([^"]*)"$`, func(a, b string, pos int, text string) error {
		show = selectionShow([]string{a, b}, []int{pos}, &text)
		return nil
	})
	ctx.Step(`^a choice question with options "([^"]*)" and "([^"]*)" answered with text "([^"]*)" and no selection$`, func(a, b, text string) error {
		show = selectionShow([]string{a, b}, []int{}, &text)
		return nil
	})
	ctx.Step(`^a choice question with options "([^"]*)", "([^"]*)" and "([^"]*)" answered by selecting positions (\d+) and (\d+)$`, func(a, b, c string, p1, p2 int) error {
		show = selectionShow([]string{a, b, c}, []int{p1, p2}, nil)
		return nil
	})
	ctx.Step(`^a choice question answered with an empty selection and no text$`, func() error {
		show = selectionShow([]string{"Yes", "No"}, []int{}, nil)
		return nil
	})
	ctx.Step(`^read\.sh reads that choice question$`, func() error {
		// Reuses the real-shape world (and its exits/no-answer steps).
		w := activeRealShape
		if err := w.fixture(show); err != nil {
			return err
		}
		_, err := w.phase("read.sh", map[string]string{"question_id": "Q1"})
		return err
	})
	ctx.Step(`^the read output is exactly "([^"]*)"$`, func(want string) error { return readAnswerIs(want + "\n") })
	ctx.Step(`^the read output is "([^"]*)" followed by the line "([^"]*)"$`, func(first, second string) error {
		return readAnswerIs(first + "\n" + second + "\n")
	})
	var row string
	ctx.Step(`^I read the read-answer row of the saga module README$`, func() error {
		b, err := os.ReadFile(filepath.Join("..", ".cloche", "modules", "saga", "README.md"))
		if err != nil {
			return err
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "| `read-answer`") {
				row = l
				return nil
			}
		}
		return fmt.Errorf("no read-answer row in README")
	})
	ctx.Step(`^it says the output has the selected option labels and the text$`, func() error {
		low := strings.ToLower(row)
		if !strings.Contains(low, "selected") || !strings.Contains(low, "label") || !strings.Contains(low, "text") {
			return fmt.Errorf("row does not mention selected labels and text: %s", row)
		}
		return nil
	})
}

// readAnswerIs checks the exact file content real read.sh wrote.
func readAnswerIs(want string) error {
	w := activeRealShape
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
	if string(got) != want {
		return fmt.Errorf("answer = %q, want %q", got, want)
	}
	return nil
}

// selectionShow renders `saga show --json` in the real shape: options carry
// position+label, an answer has "select" positions and no "text" key unless
// text was typed.
func selectionShow(labels []string, sel []int, text *string) string {
	opts := []map[string]any{}
	for i, l := range labels {
		opts = append(opts, map[string]any{"position": i + 1, "label": l})
	}
	a := map[string]any{"author": "human", "select": sel, "status": nil}
	if text != nil {
		a["text"] = *text
	}
	b, err := json.Marshal(map[string]any{"id": "Q1", "status": map[string]any{"state": "in-force"},
		"options": opts, "answers": []any{a}})
	if err != nil {
		panic(err) // fixed test data; cannot fail to marshal
	}
	return string(b)
}

// selectionShapes is the stub saga's repertoire of answer shapes.
func selectionShapes() map[string]string {
	t1, t2 := "Go ahead", "Maybe later"
	yn := []string{"Yes", "No"}
	return map[string]string{
		"selection-only": selectionShow(yn, []int{1}, nil),
		"selection+text": selectionShow(yn, []int{1}, &t1),
		"text-only":      selectionShow(yn, []int{}, &t2),
		"multi-select":   selectionShow([]string{"A", "B", "C"}, []int{1, 2}, nil),
	}
}

// unfixedReadSh is read.sh as it was before selection support: it only prints .text.
const unfixedReadSh = `#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$here/lib.sh"
id="$(cloche get question_id)"
json="$(saga_show "$id")"
text="$(printf '%s' "$json" | jq -r '[(.answers // [])[]] | last | .text // empty')"
if [ -z "$text" ]; then echo "error: no answer for question $id" >&2; exit 1; fi
path="$(mktemp "${TMPDIR:-/tmp}/saga-answer-XXXXXX")"
printf '%s\n' "$text" > "$path"
cloche set answer_path "$path"
`

// readWithUnfixedScript runs the pre-fix read.sh against a saga stub printing
// showJSON and returns an error unless it produced exactly want.
func readWithUnfixedScript(showJSON, want string) error {
	dir, err := os.MkdirTemp("", "saga-unfixed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	scripts := filepath.Join(dir, "scripts")
	bin := filepath.Join(dir, "bin")
	kv := filepath.Join(dir, "kv")
	for _, d := range []string{scripts, bin, kv} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	lib, err := os.ReadFile(filepath.Join("..", ".cloche", "modules", "saga", "scripts", "lib.sh"))
	if err != nil {
		return err
	}
	files := map[string]string{
		filepath.Join(scripts, "lib.sh"):  string(lib),
		filepath.Join(scripts, "read.sh"): unfixedReadSh,
		filepath.Join(dir, "show.json"):   showJSON,
		filepath.Join(bin, "saga"):        "#!/usr/bin/env bash\ncat " + filepath.Join(dir, "show.json") + "\n",
		filepath.Join(bin, "cloche"):      sagaScenarioKV,
		filepath.Join(kv, "question_id"):  "Q1",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			return err
		}
	}
	cmd := exec.Command("bash", filepath.Join(scripts, "read.sh"))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KV_DIR="+kv, "TMPDIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("read.sh failed: %v: %s", err, out)
	}
	path, err := os.ReadFile(filepath.Join(kv, "answer_path"))
	if err != nil {
		return err
	}
	got, err := os.ReadFile(string(path))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(got)) != want {
		return fmt.Errorf("answer = %q, want %q", got, want)
	}
	return nil
}
