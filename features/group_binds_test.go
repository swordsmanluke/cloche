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

// gbState is the per-scenario state for the group-binds feature. Modules are
// fake: in-memory for resolution, written to a temp project for validation.
type gbState struct {
	modules      map[string]gbModule
	bindText     []string // bind lines written in the workflow's file scope
	wfName       string
	target       string // validate: target is a non-module workflow
	resolved     map[string]*module.Resolved
	resolveErr   map[string]error
	binds        []domain.Bind
	parseErr     error
	report       *module.Report
	existingSrc  string
	existingWant []domain.Bind
}

type gbModule struct {
	exports []string
	creds   []string
}

var gb *gbState

func groupBindsPending(layer string) error { return godog.ErrPending }

func (s *gbState) addModule(name string, exports []string, creds ...string) {
	s.modules[name] = gbModule{exports: exports, creds: creds}
}

func (s *gbState) parseBinds(lines []string) ([]domain.Bind, error) {
	src := strings.Join(lines, "\n") + "\nworkflow w {\n  step a {\n    run = \"true\"\n    results = [success, fail]\n  }\n  a:success -> done\n  a:fail -> abort\n}\n"
	f, err := dsl.ParseFile(src)
	if err != nil {
		return nil, err
	}
	return f.Binds, nil
}

func (s *gbState) resolver() *module.Resolver {
	reg := &module.Registry{Modules: map[string]*module.Module{}}
	for name, m := range s.modules {
		wfs := map[string]*domain.Workflow{}
		for _, e := range m.exports {
			wfs[e] = &domain.Workflow{Name: e, Module: name}
		}
		reg.Modules[name] = &module.Module{
			Manifest:  module.Manifest{Name: name, Exports: m.exports, Credentials: m.creds},
			Workflows: wfs,
		}
	}
	return &module.Resolver{Registry: reg}
}

func (s *gbState) resolveAll(binds []domain.Bind) map[string]*module.Resolved {
	rs := s.resolver()
	stack := module.CallStack{{Workflow: "w", Scopes: []module.Scope{{Kind: module.ScopeFile, Binds: binds}}}}
	out := map[string]*module.Resolved{}
	for _, slots := range domain.BindGroups {
		for _, slot := range slots {
			if r, err := rs.ResolveSlot(stack, slot); err == nil {
				out[slot] = r
			}
		}
	}
	return out
}

// validate writes the fake modules and workflow into a temp project and runs
// the same module.Check that `cloche validate` calls.
func (s *gbState) validate() error {
	dir, err := os.MkdirTemp("", "gb-validate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	w := func(path, content string) error {
		p := filepath.Join(dir, ".cloche", path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}
	const wf = "workflow %s {\n  step a {\n    run = \"true\"\n    results = [success, fail]\n  }\n  a:success -> done\n  a:fail -> abort\n}\n"
	for name, m := range s.modules {
		var src strings.Builder
		for _, e := range m.exports {
			fmt.Fprintf(&src, wf, e)
		}
		quoted := func(l []string) string { return `["` + strings.Join(l, `","`) + `"]` }
		toml := fmt.Sprintf("name = %q\nexports = %s\n", name, quoted(m.exports))
		if len(m.creds) > 0 {
			toml += "credentials = " + quoted(m.creds) + "\n"
		}
		for path, c := range map[string]string{
			"modules/" + name + "/module.toml": toml,
			"modules/" + name + "/m.cloche":    src.String(),
			"modules/" + name + "/README.md":   "# " + name,
		} {
			if err := w(path, c); err != nil {
				return err
			}
		}
	}
	if err := w(module.ProjectFile, strings.Join(s.bindText, "\n")+"\n"); err != nil {
		return err
	}
	project := map[string]*domain.Workflow{}
	if s.wfName != "" {
		src := fmt.Sprintf(wf, s.wfName)
		if err := w("main.cloche", src); err != nil {
			return err
		}
		wfs, err := dsl.ParseAll(src)
		if err != nil {
			return err
		}
		project = wfs
	}
	s.report = module.Check(dir, project, nil)
	return nil
}

func contains(list []string, sub string) bool {
	for _, x := range list {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func initGroupBindsScenarios(ctx *godog.ScenarioContext) {
	l2 := func() error { return groupBindsPending("L2") }
	l2s := func(string) error { return groupBindsPending("L2") }
	l2ss := func(string, string) error { return groupBindsPending("L2") }

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		gb = &gbState{modules: map[string]gbModule{}}
		return c, nil
	})

	// L1
	ctx.Step(`^a module "([^"]*)" exporting ask, await-answer and read-answer$`, func(n string) error {
		gb.addModule(n, []string{"ask", "await-answer", "read-answer"})
		return nil
	})
	ctx.Step(`^a module "([^"]*)" exporting all six tasks slots$`, func(n string) error {
		gb.addModule(n, domain.BindGroups["tasks"])
		return nil
	})
	ctx.Step(`^a module "([^"]*)" exporting ask$`, func(n string) error {
		gb.addModule(n, []string{"ask"})
		return nil
	})
	ctx.Step(`^a module "([^"]*)" exporting ask and await-answer but not read-answer$`, func(n string) error {
		gb.addModule(n, []string{"ask", "await-answer"})
		return nil
	})
	ctx.Step(`^a module "([^"]*)" that lists ([A-Z_]+) as a credential$`, func(n, c string) error {
		gb.addModule(n, []string{"ask", "await-answer", "read-answer"}, c)
		return nil
	})
	ctx.Step(`^a workflow containing "(.+)"$`, func(b string) error {
		gb.bindText = append(gb.bindText, strings.ReplaceAll(b, `\"`, `"`))
		return nil
	})
	ctx.Step(`^the same scope also contains the per-slot bind "([^"]*)"$`, func(b string) error {
		gb.bindText = append(gb.bindText, b)
		return nil
	})
	ctx.Step(`^a workflow containing "([^"]*)" where the target is not a module$`, func(b string) error {
		gb.bindText = append(gb.bindText, b)
		return nil
	})
	ctx.Step(`^a workflow named "([^"]*)"$`, func(n string) error { gb.wfName = n; return nil })
	ctx.Step(`^an existing workflow file using only per-slot binds$`, func() error {
		gb.bindText = []string{"bind code-review = rev", "bind { lint = nowhere  ask = saga:ask }", "bind! review = x"}
		return nil
	})
	ctx.Step(`^the workflow is resolved$`, func() error {
		binds, err := gb.parseBinds(gb.bindText)
		if err != nil {
			return err
		}
		gb.binds = binds
		gb.resolved = gb.resolveAll(binds)
		return nil
	})
	ctx.Step(`^the workflow is parsed$`, func() error {
		gb.binds, gb.parseErr = gb.parseBinds(gb.bindText)
		return nil
	})
	expectSlot := func(slot, mod string) error {
		r := gb.resolved[slot]
		if r == nil {
			return fmt.Errorf("slot %q did not resolve", slot)
		}
		if r.Workflow.Module != mod || r.Workflow.Name != slot {
			return fmt.Errorf("slot %q resolved to %s:%s, want %s:%s", slot, r.Workflow.Module, r.Workflow.Name, mod, slot)
		}
		return nil
	}
	ctx.Step(`^slots "([^"]*)", "([^"]*)" and "([^"]*)" resolve to module "([^"]*)"$`, func(a, b, c, mod string) error {
		for _, s := range []string{a, b, c} {
			if err := expectSlot(s, mod); err != nil {
				return err
			}
		}
		return nil
	})
	ctx.Step(`^the six tasks slots resolve to module "([^"]*)"$`, func(mod string) error {
		for _, s := range domain.BindGroups["tasks"] {
			if err := expectSlot(s, mod); err != nil {
				return err
			}
		}
		return nil
	})
	ctx.Step(`^the result equals resolving the six-line per-slot form$`, func() error {
		var lines []string
		for _, s := range domain.BindGroups["tasks"] {
			lines = append(lines, "bind "+s+" = beads-tasks")
		}
		binds, err := gb.parseBinds(lines)
		if err != nil {
			return err
		}
		six := gb.resolveAll(binds)
		for _, s := range domain.BindGroups["tasks"] {
			a, b := gb.resolved[s], six[s]
			if b == nil || a == nil || a.Workflow.Module != b.Workflow.Module || a.Workflow.Name != b.Workflow.Name {
				return fmt.Errorf("slot %q: group form and six-line form differ", s)
			}
		}
		return nil
	})
	ctx.Step(`^slot "([^"]*)" resolves to module "([^"]*)"$`, expectSlot)
	ctx.Step(`^parsing succeeds$`, func() error { return gb.parseErr })
	ctx.Step(`^its binds are unchanged$`, func() error {
		for _, b := range gb.binds {
			if b.Group != "" || b.Env != nil {
				return fmt.Errorf("bind %s = %s unexpectedly carries group/env", b.Slot, b.Target)
			}
		}
		if len(gb.binds) != 4 {
			return fmt.Errorf("got %d binds, want 4", len(gb.binds))
		}
		return nil
	})
	ctx.Step(`^the user runs "cloche validate"$`, func() error { return gb.validate() })
	ctx.Step(`^validation fails with an error naming "([^"]*)"$`, func(sub string) error {
		if !contains(gb.report.Errors, sub) {
			return fmt.Errorf("no error naming %q: %v", sub, gb.report.Errors)
		}
		return nil
	})
	ctx.Step(`^validation fails with an error saying the target must be a module$`, func() error {
		if !contains(gb.report.Errors, "must be") || !contains(gb.report.Errors, "module") {
			return fmt.Errorf("no must-be-a-module error: %v", gb.report.Errors)
		}
		return nil
	})
	ctx.Step(`^validation fails with an error saying "([^"]*)" is a reserved group name$`, func(n string) error {
		if !contains(gb.report.Errors, fmt.Sprintf("%q is a reserved group name", n)) {
			return fmt.Errorf("no reserved-name error: %v", gb.report.Errors)
		}
		return nil
	})
	ctx.Step(`^validation succeeds with a warning naming "([^"]*)"$`, func(sub string) error {
		if len(gb.report.Errors) > 0 {
			return fmt.Errorf("validation failed: %v", gb.report.Errors)
		}
		if !contains(gb.report.Warnings, sub) {
			return fmt.Errorf("no warning naming %q: %v", sub, gb.report.Warnings)
		}
		return nil
	})

	// L2
	ctx.Step(`^a stub module "([^"]*)" whose slots echo their environment$`, l2s)
	ctx.Step(`^a stub module "([^"]*)" whose contract requires env ([A-Z_]+)$`, l2ss)
	ctx.Step(`^the daemon environment sets ([A-Z_]+) to "([^"]*)"$`, l2ss)
	ctx.Step(`^an outer-scope bind sets ([A-Z_]+) to "([^"]*)"$`, l2ss)
	ctx.Step(`^an inner-scope bind sets ([A-Z_]+) to "([^"]*)"$`, l2ss)
	ctx.Step(`^a step runs the "([^"]*)" slot$`, l2s)
	ctx.Step(`^a step runs the "([^"]*)" slot with a with block setting ([A-Z_]+) to "([^"]*)"$`,
		func(string, string, string) error { return groupBindsPending("L2") })
	ctx.Step(`^a step runs the "([^"]*)" slot on both the daemon and host paths$`, l2s)
	ctx.Step(`^the slot's environment has ([A-Z_]+) = "([^"]*)"$`, l2ss)
	ctx.Step(`^the run is not rejected for a missing ([A-Z_]+)$`, l2s)
	_ = l2
}
