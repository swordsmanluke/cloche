package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dsl"
	"github.com/swordsmanluke/cloche/internal/module"
)

func init() { registerScenarios(initGroupBindsScenarios) }


// gbState is the per-scenario project built up by the L1 steps: modules and
// workflows are recorded first and written to a temp dir when validate or
// resolve runs.
type gbState struct {
	mods     []gbModule
	wfs      []gbWorkflow
	report   *module.Report
	valErr   error
	resolved map[string]string // "<wf>/<slot>" -> module of the resolved workflow
	repoRoot bool

	// L2: bind env reaching slot runs.
	rootBinds []string          // project.cloche (outer scope) binds
	daemon    map[string]string // the daemon environment
	runEnv    map[string]string // env seen by the most recent slot run
	runs      []map[string]string
	contract  string // env var a module's contract requires
	runErr    error
}

type gbModule struct {
	name    string
	exports []string
	creds   []string
}

type gbWorkflow struct{ name, binds string }

var gbs *gbState

func gbSplit(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (g *gbState) addModule(name string, exports []string) {
	g.mods = append(g.mods, gbModule{name: name, exports: exports})
}

// addWorkflow records a workflow whose file-level binds are the given lines.
func (g *gbState) addWorkflow(name string, bindLines ...string) {
	g.wfs = append(g.wfs, gbWorkflow{name: name, binds: strings.Join(bindLines, "\n")})
}

// bindLines turns the step argument — `"bind a = b"`, `'bind a = b { ... }'`
// or `"bind a = b" and "bind c = d"` — into bind statements.
func gbBindLines(arg string) []string {
	var out []string
	for _, part := range strings.Split(arg, `" and "`) {
		out = append(out, strings.Trim(part, `"'`))
	}
	return out
}

const gbStep = "  step a {\n    run = \"true\"\n    results = [success, fail]\n  }\n  a:success -> done\n  a:fail -> abort\n"

// materialize writes the recorded project to a temp dir and returns it.
func (g *gbState) materialize() (string, error) {
	if g.repoRoot {
		return "..", nil
	}
	dir, err := os.MkdirTemp("", "gbind")
	if err != nil {
		return "", err
	}
	write := func(rel, content string) error {
		p := filepath.Join(dir, ".cloche", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}
	for _, m := range g.mods {
		toml := fmt.Sprintf("name = %q\nexports = [%s]\n", m.name, quoteList(m.exports))
		if len(m.creds) > 0 {
			toml += fmt.Sprintf("credentials = [%s]\n", quoteList(m.creds))
		}
		if err := write("modules/"+m.name+"/module.toml", toml); err != nil {
			return "", err
		}
		if err := write("modules/"+m.name+"/README.md", "# "+m.name); err != nil {
			return "", err
		}
		var src strings.Builder
		for _, e := range m.exports {
			contract := ""
			if g.contract != "" {
				contract = "  contract {\n    env: " + g.contract + "\n  }\n"
			}
			fmt.Fprintf(&src, "workflow %s {\n%s%s}\n", e, contract, gbStep)
		}
		if err := write("modules/"+m.name+"/m.cloche", src.String()); err != nil {
			return "", err
		}
	}
	if len(g.rootBinds) > 0 {
		if err := write(module.ProjectFile, strings.Join(g.rootBinds, "\n")+"\n"); err != nil {
			return "", err
		}
	}
	for _, w := range g.wfs {
		if err := write(w.name+".cloche", w.binds+"\nworkflow "+w.name+" {\n"+gbStep+"}\n"); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func quoteList(l []string) string {
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// parseProject parses every non-project .cloche file under dir/.cloche.
func gbParse(dir string) (map[string]*domain.Workflow, error) {
	files, _ := filepath.Glob(filepath.Join(dir, ".cloche", "*.cloche"))
	out := map[string]*domain.Workflow{}
	for _, p := range files {
		if filepath.Base(p) == module.ProjectFile {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		wfs, err := dsl.ParseAll(string(data), dsl.WithSourceFile(p))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for n, wf := range wfs {
			out[n] = wf
		}
	}
	return out, nil
}

func (g *gbState) validate() error {
	dir, err := g.materialize()
	if err != nil {
		return err
	}
	proj, err := gbParse(dir)
	if err != nil {
		g.valErr = err
		return nil
	}
	g.report = module.Check(dir, proj, nil)
	return nil
}

// resolve resolves slot at the top of workflow wf's bind chain and returns the
// module owning the chosen workflow.
func (g *gbState) resolve(wf, slot string) (string, error) {
	res, err := g.resolveFull(wf, slot)
	if err != nil {
		return "", err
	}
	return res.Workflow.Module, nil
}

func (g *gbState) resolveFull(wf, slot string) (*module.Resolved, error) {
	dir, err := g.materialize()
	if err != nil {
		return nil, err
	}
	proj, err := gbParse(dir)
	if err != nil {
		return nil, err
	}
	reg, err := module.Load(dir)
	if err != nil {
		return nil, err
	}
	w := proj[wf]
	if w == nil {
		return nil, fmt.Errorf("no gbind workflow %q", wf)
	}
	rs := &module.Resolver{Registry: reg, Project: proj}
	res, err := rs.ResolveSlot(module.CallStack{reg.FrameFor(w, nil)}, slot)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (g *gbState) wfName(i int) string { return g.wfs[i].name }

func gbWant(g *gbState, wf string, slots []string, mod string) error {
	for _, s := range slots {
		got, err := g.resolve(wf, s)
		if err != nil {
			return fmt.Errorf("slot %s: %w", s, err)
		}
		if got != mod {
			return fmt.Errorf("slot %s resolved to module %q, want %q", s, got, mod)
		}
	}
	return nil
}

func gbErrContains(g *gbState, sub string) error {
	if g.valErr != nil {
		return fmt.Errorf("validate failed to parse: %w", g.valErr)
	}
	for _, e := range g.report.Errors {
		if strings.Contains(e, sub) {
			return nil
		}
	}
	return fmt.Errorf("no validation error contains %q; errors: %v", sub, g.report.Errors)
}

func initGroupBindsScenarios(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		gbs = &gbState{}
		return c, nil
	})

	// L1 steps (parse / resolve / validate)
	ctx.Step(`^a gbind project with a module "([^"]*)" exporting ask, await-answer and read-answer$`, func(name string) error {
		gbs.addModule(name, []string{"ask", "await-answer", "read-answer"})
		return nil
	})
	ctx.Step(`^a gbind workflow containing (.+)$`, func(arg string) error {
		gbs.addWorkflow(fmt.Sprintf("wf%d", len(gbs.wfs)+1), gbBindLines(arg)...)
		return nil
	})
	ctx.Step(`^the gbind user runs "([^"]*)"$`, func(cmd string) error {
		if cmd != "cloche validate" {
			return fmt.Errorf("unsupported command %q", cmd)
		}
		return gbs.validate()
	})
	ctx.Step(`^the gbind validation succeeds$`, func() error {
		if gbs.valErr != nil {
			return gbs.valErr
		}
		if len(gbs.report.Errors) > 0 {
			return fmt.Errorf("validation errors: %v", gbs.report.Errors)
		}
		return nil
	})
	ctx.Step(`^the gbind slots "([^"]*)" resolve to module "([^"]*)"$`, func(slots, mod string) error {
		return gbWant(gbs, gbs.wfName(0), gbSplit(slots), mod)
	})
	ctx.Step(`^a gbind module "([^"]*)" exporting list-tasks, claim-task, close-task, release-task, create-task and update-task$`, func(name string) error {
		gbs.addModule(name, domain.BindGroups["tasks"])
		return nil
	})
	ctx.Step(`^a second gbind workflow with six per-slot binds to "([^"]*)"$`, func(mod string) error {
		var lines []string
		for _, s := range domain.BindGroups["tasks"] {
			lines = append(lines, fmt.Sprintf("bind %s = %s", s, mod))
		}
		gbs.addWorkflow(fmt.Sprintf("wf%d", len(gbs.wfs)+1), lines...)
		return nil
	})
	ctx.Step(`^the gbind user resolves the six task slots in both workflows$`, func() error {
		gbs.resolved = map[string]string{}
		for i := range gbs.wfs {
			for _, s := range domain.BindGroups["tasks"] {
				m, err := gbs.resolve(gbs.wfName(i), s)
				if err != nil {
					return err
				}
				gbs.resolved[gbs.wfName(i)+"/"+s] = m
			}
		}
		return nil
	})
	ctx.Step(`^both workflows resolve every task slot to "([^"]*)"$`, func(mod string) error {
		if len(gbs.wfs) != 2 {
			return fmt.Errorf("expected two workflows, have %d", len(gbs.wfs))
		}
		for k, m := range gbs.resolved {
			if m != mod {
				return fmt.Errorf("%s resolved to %q, want %q", k, m, mod)
			}
		}
		return nil
	})
	ctx.Step(`^a gbind module "([^"]*)" exporting ask$`, func(name string) error {
		gbs.addModule(name, []string{"ask"})
		return nil
	})
	ctx.Step(`^the gbind user resolves the qa slots$`, func() error { return nil }) // resolved lazily by the assertions below
	ctx.Step(`^slot "([^"]*)" resolves to module "([^"]*)"$`, func(slot, mod string) error {
		return gbWant(gbs, gbs.wfName(0), []string{slot}, mod)
	})
	ctx.Step(`^slots "([^"]*)" resolve to module "([^"]*)"$`, func(slots, mod string) error {
		return gbWant(gbs, gbs.wfName(0), gbSplit(slots), mod)
	})
	ctx.Step(`^a gbind module "([^"]*)" exporting ask only$`, func(name string) error {
		gbs.addModule(name, []string{"ask"})
		return nil
	})
	ctx.Step(`^the gbind validation fails with an error naming "([^"]*)"$`, func(sub string) error {
		return gbErrContains(gbs, sub)
	})
	ctx.Step(`^the gbind validation fails with an error saying the target must be a module$`, func() error {
		return gbErrContains(gbs, "must be an installed module")
	})
	ctx.Step(`^a gbind workflow named "([^"]*)"$`, func(name string) error {
		gbs.addWorkflow(name)
		return nil
	})
	ctx.Step(`^the gbind validation fails with an error saying "([^"]*)" is reserved$`, func(name string) error {
		return gbErrContains(gbs, fmt.Sprintf("%q is a reserved", name))
	})
	ctx.Step(`^the gbind module "([^"]*)" lists ([A-Z_]+) as a credential$`, func(name, cred string) error {
		for i := range gbs.mods {
			if gbs.mods[i].name == name {
				gbs.mods[i].creds = append(gbs.mods[i].creds, cred)
				return nil
			}
		}
		return fmt.Errorf("no gbind module %q", name)
	})
	ctx.Step(`^the gbind validation succeeds with a warning naming "([^"]*)"$`, func(sub string) error {
		if gbs.valErr != nil || len(gbs.report.Errors) > 0 {
			return fmt.Errorf("validation failed: %v %v", gbs.valErr, gbs.report.Errors)
		}
		for _, w := range gbs.report.Warnings {
			if strings.Contains(w, sub) {
				return nil
			}
		}
		return fmt.Errorf("no warning names %q: %v", sub, gbs.report.Warnings)
	})
	ctx.Step(`^the gbind repository's existing \.cloche files$`, func() error {
		gbs.repoRoot = true
		return nil
	})
	ctx.Step(`^the gbind validation succeeds with no new errors$`, func() error {
		// The repo's own modules have environment-dependent errors (missing
		// binaries); only bind-syntax and group-bind errors are in scope here.
		if gbs.valErr != nil {
			return gbs.valErr
		}
		for _, e := range gbs.report.Errors {
			if strings.Contains(e, "reserved") || strings.Contains(e, "group") || strings.Contains(e, "bind") {
				return fmt.Errorf("unexpected bind error: %s", e)
			}
		}
		return nil
	})

	// L2 steps (bind env reaches runs). Both run paths share
	// module.ResolveSlot + module.MergeEnv + module.CheckInputs; the real
	// executor/phase runs are covered by unit tests in internal/adapters/grpc
	// and internal/host.
	var withEnv map[string]string
	ctx.Step(`^the gbind slot "([^"]*)" runs via the "([^"]*)" path$`, func(slot, path string) error {
		if path != "grpc executor" && path != "host phases" {
			return fmt.Errorf("unknown run path %q", path)
		}
		return gbs.run(slot, nil, true)
	})
	ctx.Step(`^the run sees ([A-Z_]+) equal to "([^"]*)"$`, func(k, v string) error {
		if gbs.runErr != nil {
			return gbs.runErr
		}
		if gbs.runEnv[k] != v {
			return fmt.Errorf("run sees %s=%q, want %q", k, gbs.runEnv[k], v)
		}
		return nil
	})
	ctx.Step(`^the daemon environment sets X to "([^"]*)"$`, func(v string) error {
		gbs.daemon = map[string]string{"X": v}
		return nil
	})
	ctx.Step(`^an outer-scope bind sets env\.X to "([^"]*)"$`, func(v string) error {
		gbs.rootBinds = append(gbs.rootBinds, fmt.Sprintf("bind ask = saga { env.X = %q }", v))
		return nil
	})
	ctx.Step(`^an inner-scope bind sets env\.X to "([^"]*)"$`, func(v string) error {
		gbs.addWorkflow("user", fmt.Sprintf("bind ask = saga { env.X = %q }", v))
		return nil
	})
	ctx.Step(`^the gbind slot runs with a step with-block setting X to "([^"]*)"$`, func(v string) error {
		withEnv = map[string]string{"X": v}
		return gbs.run("ask", withEnv, false)
	})
	ctx.Step(`^the gbind slot runs without a with-block$`, func() error {
		return gbs.run("ask", nil, false)
	})
	ctx.Step(`^the gbind user runs each of ask, await-answer and read-answer$`, func() error {
		gbs.runs = nil
		for _, s := range []string{"ask", "await-answer", "read-answer"} {
			if err := gbs.run(s, nil, true); err != nil {
				return err
			}
			gbs.runs = append(gbs.runs, gbs.runEnv)
		}
		return nil
	})
	ctx.Step(`^every run sees ([A-Z_]+) equal to "([^"]*)"$`, func(k, v string) error {
		if len(gbs.runs) != 3 {
			return fmt.Errorf("expected 3 runs, got %d", len(gbs.runs))
		}
		for i, r := range gbs.runs {
			if r[k] != v {
				return fmt.Errorf("run %d sees %s=%q, want %q", i, k, r[k], v)
			}
		}
		return nil
	})
	ctx.Step(`^the gbind module "([^"]*)" contract requires env ([A-Z_]+)$`, func(_, name string) error {
		gbs.contract = name
		return nil
	})
	ctx.Step(`^([A-Z_]+) is not set in the daemon environment$`, func(string) error { return nil })
	ctx.Step(`^the gbind slot "([^"]*)" runs$`, func(slot string) error {
		return gbs.run(slot, nil, true)
	})
	ctx.Step(`^the run is not rejected for a missing env requirement$`, func() error { return gbs.runErr })
}

// run resolves slot through the first recorded workflow (or "user"), checks
// the callee's contract env, and records the env the run would see: daemon
// env, then bind settings, then the step's with env.
func (g *gbState) run(slot string, with map[string]string, _ bool) error {
	if len(g.wfs) == 0 {
		g.addWorkflow("user")
	}
	wf := g.wfs[len(g.wfs)-1].name
	res, err := g.resolveFull(wf, slot)
	if err != nil {
		return err
	}
	env := module.MergeEnv(g.daemon, res.Env, with)
	g.runEnv = env
	has := func(n string) bool { _, ok := env[n]; return ok }
	g.runErr = module.CheckInputs(slot, res.Workflow.Contract, func(string) bool { return true }, has)
	return nil
}
