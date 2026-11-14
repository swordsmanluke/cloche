package features_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initSagaQAScenarios) }

// L1 steps run the saga module's scripts directly (the bind's SAGA_PROJECT is
// simulated as an env var) against a stub saga on PATH. The real saga binary
// is never used. The cloche CLI/daemon path is covered by questions_cli.
const sagaScenarioStub = `#!/usr/bin/env bash
set -euo pipefail
s="${SAGA_STUB_DIR:?}"; mkdir -p "$s"
echo "$@" >> "$s/log"
case "$1" in
  ask)
    shift; file=""
    while [ $# -gt 0 ]; do case "$1" in --file) file="$2"; shift ;; esac; shift; done
    n=$(jq '.questions | length' "$file"); ids=()
    for _ in $(seq "$n"); do
      c=$(( $(cat "$s/next" 2>/dev/null || echo 0) + 1 )); echo "$c" > "$s/next"
      echo '[]' > "$s/Q$c.answers"; ids+=("Q$c")
    done
    printf '%s\n' "${ids[@]}" | jq -R . | jq -sc '{batch:"B1",questions:.}' ;;
  show)
    jq -c --arg id "$2" '{id:$id,status:{state:(if length>0 then "in-force" else "open" end)},answers:.}' "$s/$2.answers" ;;
  answer)
    jq -c --arg k "$3" --arg t "$4" '. + [{author:{kind:$k},text:$t}]' "$s/$2.answers" > "$s/$2.tmp" && mv "$s/$2.tmp" "$s/$2.answers" ;;
  *) exit 2 ;;
esac
`

const sagaScenarioKV = `#!/usr/bin/env bash
case "$1" in
  get) [ -e "$KV_DIR/$2" ] || exit 1; cat "$KV_DIR/$2" ;;
  set) printf '%s' "$3" > "$KV_DIR/$2" ;;
esac
`

type sagaWorld struct {
	dir     string
	feature string
	ids     []string
	out     string
	rc      error
	took    time.Duration
	open    []string
	answer  string
}

func (w *sagaWorld) mod() string {
	d, _ := filepath.Abs(filepath.Join("..", ".cloche", "modules", "saga"))
	return d
}

func (w *sagaWorld) setup() error {
	d, err := os.MkdirTemp("", "saga-scn-")
	if err != nil {
		return err
	}
	w.dir = d
	if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
		return err
	}
	return nil
}

func (w *sagaWorld) installStub() error {
	for n, body := range map[string]string{"saga": sagaScenarioStub, "cloche": sagaScenarioKV} {
		if err := os.WriteFile(filepath.Join(w.dir, "bin", n), []byte(body), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (w *sagaWorld) env() []string {
	return append(os.Environ(),
		"PATH="+filepath.Join(w.dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SAGA_STUB_DIR="+filepath.Join(w.dir, "state"), "KV_DIR="+filepath.Join(w.dir, "kv"),
		"CLOCHE_MODULE_DIR="+w.mod(), "SAGA_PROJECT=cloche", "TMPDIR="+w.dir)
}

// phase runs a module script with kv inputs and returns kv outputs.
func (w *sagaWorld) phase(script string, in map[string]string) (map[string]string, error) {
	kv := filepath.Join(w.dir, "kv")
	os.RemoveAll(kv)
	if err := os.MkdirAll(kv, 0o755); err != nil {
		return nil, err
	}
	for k, v := range in {
		if err := os.WriteFile(filepath.Join(kv, k), []byte(v), 0o644); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command("bash", filepath.Join(w.mod(), "scripts", script))
	cmd.Env = w.env()
	start := time.Now()
	out, err := cmd.CombinedOutput()
	w.took, w.out, w.rc = time.Since(start), string(out), err
	res := map[string]string{}
	entries, _ := os.ReadDir(kv)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(kv, e.Name()))
		res[e.Name()] = string(b)
	}
	return res, nil
}

func (w *sagaWorld) writeQuestions(feature string) (string, error) {
	f := filepath.Join(w.dir, "questions.json")
	body := `{"title":"T","context":"C","questions":[{"prompt":"one"},{"prompt":"two"}]`
	if feature != "" {
		body += `,"backend":{"feature":"` + feature + `"}`
	}
	return f, os.WriteFile(f, []byte(body+"}"), 0o644)
}

func (w *sagaWorld) ask() error {
	if w.feature == "" {
		w.feature = "cloche/qa-backends"
	}
	f, err := w.writeQuestions(w.feature)
	if err != nil {
		return err
	}
	res, _ := w.phase("ask.sh", map[string]string{"questions_file": f})
	if w.rc != nil {
		return fmt.Errorf("ask failed: %v: %s", w.rc, w.out)
	}
	w.ids = strings.Fields(res["question_ids"])
	return nil
}

func (w *sagaWorld) answerAs(id, who, text string) error {
	cmd := exec.Command(filepath.Join(w.dir, "bin", "saga"), "answer", id, who, text)
	cmd.Env = w.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stub answer: %v: %s", err, out)
	}
	return nil
}

func (w *sagaWorld) checkRC() error {
	if w.rc != nil {
		return fmt.Errorf("command failed: %v: %s", w.rc, w.out)
	}
	return nil
}

func (w *sagaWorld) runAwait() error {
	res, _ := w.phase("await.sh", map[string]string{"question_ids": strings.Join(w.ids, "\n")})
	w.open = strings.Fields(res["questions_open"])
	return w.checkRC()
}

func (w *sagaWorld) runRead() error {
	res, _ := w.phase("read.sh", map[string]string{"question_id": w.ids[0]})
	if err := w.checkRC(); err != nil {
		return err
	}
	b, err := os.ReadFile(res["answer_path"])
	w.answer = strings.TrimSpace(string(b))
	return err
}

func initSagaQAScenarios(ctx *godog.ScenarioContext) {
	w := &sagaWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = sagaWorld{}
		return c, w.setup()
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		os.RemoveAll(w.dir)
		return c, nil
	})
	w.register(ctx)
	ctx.Step(`^I\ list\ the\ modules\ under\ \.cloche/modules$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^I\ read\ the\ "([^"]*)"\ section\ of\ docs/modules\.md$`, func(a0 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ on\ this\ repository$`, func(a0 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^it\ describes\ binding\ saga\ to\ qa\ with\ SAGA_PROJECT$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^it\ does\ not\ describe\ a\ judge\ workflow\ or\ blocking\ await$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^no\ file\ in\ the\ repo\ mentions\ saga_batch_id,\ saga_batch_file,\ saga_outcome\ or\ reframe$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^there\ is\ no\ saga\-judge\ module$`, func() error {
		return godog.ErrPending // pending: L2
	})
}

func (w *sagaWorld) register(ctx *godog.ScenarioContext) {
	ctx.Step(`^a\ project\ with\ "([^"]*)"\ and\ SAGA_PROJECT\ set\ in\ the\ bind\ settings$`, func(bind string) error {
		if bind != "bind qa = saga" {
			return fmt.Errorf("unexpected bind %q", bind)
		}
		return nil // SAGA_PROJECT=cloche is supplied via env(), as the bind's settings would
	})
	ctx.Step(`^a\ stub\ saga\ binary\ on\ PATH$`, w.installStub)
	ctx.Step(`^a\ question\ file\ whose\ backend\.feature\ is\ "([^"]*)"$`, func(f string) error {
		w.feature = f
		return nil
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ against\ the\ saga\ backend$`, func(cmd string) error {
		if !strings.HasPrefix(cmd, "cloche questions ask") {
			return fmt.Errorf("unsupported command %q", cmd)
		}
		return w.ask()
	})
	ctx.Step(`^the\ saga\ scenario\ command\ exits\ 0$`, w.checkRC)
	ctx.Step(`^one\ question\ id\ per\ question\ is\ printed$`, func() error {
		if len(w.ids) != 2 {
			return fmt.Errorf("got ids %v, want 2", w.ids)
		}
		return nil
	})
	ctx.Step(`^the\ stub\ saga\ recorded\ the\ questions\ under\ the\ bind's\ SAGA_PROJECT\ and\ that\ feature$`, func() error {
		b, err := os.ReadFile(filepath.Join(w.dir, "state", "log"))
		if err != nil {
			return err
		}
		if want := "ask -p cloche -f " + w.feature + " --file "; !strings.Contains(string(b), want) {
			return fmt.Errorf("saga log %q lacks %q", b, want)
		}
		return nil
	})
	ctx.Step(`^questions\ asked\ through\ the\ saga\ backend\ that\ nobody\ has\ answered$`, w.ask)
	ctx.Step(`^questions\ asked\ through\ the\ saga\ backend\ that\ have\ all\ been\ answered$`, func() error {
		if err := w.ask(); err != nil {
			return err
		}
		for _, id := range w.ids {
			if err := w.answerAs(id, "human", "yes"); err != nil {
				return err
			}
		}
		return nil
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ over\ those\ ids\ against\ the\ saga\ backend$`, func(string) error { return w.runAwait() })
	ctx.Step(`^the\ command\ returns\ promptly$`, func() error {
		if w.took > 10*time.Second {
			return fmt.Errorf("await took %v", w.took)
		}
		return nil
	})
	ctx.Step(`^the\ saga\ backend\ reports\ those\ questions\ as\ open$`, func() error {
		if strings.Join(w.open, ",") != strings.Join(w.ids, ",") {
			return fmt.Errorf("open = %v, want %v", w.open, w.ids)
		}
		return nil
	})
	ctx.Step(`^the\ saga\ backend\ reports\ no\ open\ questions$`, func() error {
		if len(w.open) != 0 {
			return fmt.Errorf("open = %v, want none", w.open)
		}
		return nil
	})
	ctx.Step(`^a\ saga\ question\ with\ a\ judge\ ruling$`, func() error {
		if err := w.ask(); err != nil {
			return err
		}
		return w.answerAs(w.ids[0], "judge", "judge says")
	})
	ctx.Step(`^a\ saga\ question\ with\ both\ a\ judge\ ruling\ and\ a\ human\ answer$`, func() error {
		if err := w.ask(); err != nil {
			return err
		}
		if err := w.answerAs(w.ids[0], "human", "human says"); err != nil {
			return err
		}
		return w.answerAs(w.ids[0], "judge", "judge says")
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ for\ that\ id\ against\ the\ saga\ backend$`, func(string) error { return w.runRead() })
	ctx.Step(`^the\ printed\ answer\ path\ holds\ the\ judge\ ruling$`, func() error { return w.wantAnswer("judge says") })
	ctx.Step(`^the\ printed\ answer\ path\ holds\ the\ human\ answer$`, func() error { return w.wantAnswer("human says") })
	ctx.Step(`^the\ shared\ qa\ conformance\ test\ runs\ the\ saga\ module\ with\ the\ stub\ saga$`, func() error {
		cmd := exec.Command("go", "test", "../internal/module/", "-run", "TestSagaModulePassesQAConformance", "-count=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			// "every conformance check passes" is bound in questions_cli_test.go
			// (first match wins), so a failure is reported here.
			return fmt.Errorf("saga module failed the qa conformance suite: %v\n%s", err, out)
		}
		return nil
	})
	ctx.Step(`^I\ list\ the\ saga\ module's\ files$`, func() error {
		_, err := os.Stat(w.mod())
		return err
	})
	ctx.Step(`^judge\.md\ and\ asking\.md\ exist$`, func() error {
		for _, f := range []string{"judge.md", "asking.md"} {
			if _, err := os.Stat(filepath.Join(w.mod(), f)); err != nil {
				return err
			}
		}
		return nil
	})
	ctx.Step(`^judge\.md\ mentions\ "([^"]*)",\ "([^"]*)",\ "([^"]*)"\ and\ "([^"]*)"$`, func(a, b, c, d string) error {
		data, err := os.ReadFile(filepath.Join(w.mod(), "judge.md"))
		if err != nil {
			return err
		}
		for _, m := range []string{a, b, c, d} {
			if !strings.Contains(string(data), m) {
				return fmt.Errorf("judge.md lacks %q", m)
			}
		}
		return nil
	})
}

func (w *sagaWorld) wantAnswer(want string) error {
	if w.answer != want {
		return fmt.Errorf("answer = %q, want %q", w.answer, want)
	}
	return nil
}
