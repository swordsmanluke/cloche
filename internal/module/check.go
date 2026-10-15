package module

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// Report is the outcome of Check: Errors fail `cloche validate`, Warnings
// are advisory.
type Report struct {
	Errors   []string
	Warnings []string
}

func (r *Report) errorf(format string, a ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, a...))
}
func (r *Report) warnf(format string, a ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, a...))
}
func (r *Report) merge(o *Report) {
	r.Errors = append(r.Errors, o.Errors...)
	r.Warnings = append(r.Warnings, o.Warnings...)
}
func (r *Report) sorted() *Report { sort.Strings(r.Errors); sort.Strings(r.Warnings); return r }

// seededKeys are the KV keys every run is seeded with; the cloche.* namespace
// is also always available.
var seededKeys = map[string]bool{
	"task_id": true, "attempt_id": true, "workflow": true, "run_id": true, "temp_file_dir": true,
}

// ContentHash returns the content hash of a module directory as recorded in
// modules.lock: "sha256:" plus the hex digest over every regular file's
// slash-separated relative path and bytes, in sorted order, skipping .git.
func ContentHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// MissingRequires returns the module's required binaries that are not on PATH.
func (m *Module) MissingRequires() []string {
	var missing []string
	for _, b := range m.Requires {
		if _, err := exec.LookPath(b); err != nil {
			missing = append(missing, b)
		}
	}
	return missing
}

// MissingCredentials returns the module's credentials that are neither set in
// the environment nor passed through CLOCHE_EXTRA_ENV.
func (m *Module) MissingCredentials() []string {
	extra := map[string]bool{}
	for _, kv := range strings.Split(os.Getenv("CLOCHE_EXTRA_ENV"), ",") {
		if k, _, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
			extra[k] = true
		}
	}
	var missing []string
	for _, c := range m.Credentials {
		if os.Getenv(c) == "" && !extra[c] {
			missing = append(missing, c)
		}
	}
	return missing
}

// ResolveBind resolves a bind's target as written in a scope belonging to
// module ("" for project files).
func (rs *Resolver) ResolveBind(b domain.Bind, module string) (*domain.Workflow, error) {
	return rs.resolveTarget(b, Scope{Module: module})
}

// Check validates the modules, binds and contracts of a project. project
// holds the project's workflows keyed by name; builtin may be nil.
func Check(projectDir string, project map[string]*domain.Workflow, builtin func(string) (*domain.Workflow, bool)) *Report {
	rep := &Report{}
	reg, err := Load(projectDir)
	if err != nil {
		rep.errorf("modules: %v", err)
		return rep
	}
	rs := &Resolver{Registry: reg, Project: project, Builtin: builtin}

	checkModules(projectDir, reg, rep)
	checkBinds(reg, rs, rep)

	for _, name := range sortedKeys(project) {
		checkWorkflow(project[name], rs, reg, rep)
	}
	for _, mn := range reg.Names() {
		m := reg.Modules[mn]
		for _, name := range sortedKeys(m.Workflows) {
			checkWorkflow(m.Workflows[name], rs, reg, rep)
		}
	}
	return rep.sorted()
}

func checkModules(projectDir string, reg *Registry, rep *Report) {
	lock, _ := readLock(filepath.Join(projectDir, ".cloche", LockFile))
	locked := map[string]LockEntry{}
	for _, e := range lock {
		locked[e.Name] = e
	}
	for _, mn := range reg.Names() {
		m := reg.Modules[mn]
		if len(m.Exports) == 0 {
			rep.warnf("module %q: module.toml exports no workflows", mn)
		}
		for _, e := range m.Exports {
			wf, ok := m.Workflows[e]
			if !ok {
				rep.errorf("module %q: exports %q but defines no such workflow", mn, e)
				continue
			}
			if wf.Contract == nil {
				rep.warnf("module %q: exported workflow %q has no contract", mn, e)
			}
		}
		if matches, _ := filepath.Glob(filepath.Join(m.Dir, "README*")); len(matches) == 0 {
			rep.errorf("module %q: missing README", mn)
		}
		for _, b := range m.MissingRequires() {
			rep.errorf("module %q: required binary %q not found on PATH", mn, b)
		}
		for _, c := range m.MissingCredentials() {
			rep.warnf("module %q: credential %q is not set", mn, c)
		}
		if e, ok := locked[mn]; ok && e.Hash != "" && !m.Local {
			if got, err := ContentHash(m.Dir); err == nil && got != e.Hash {
				rep.warnf("module %q: content differs from modules.lock hash (local edits? `module update` will overwrite them)", mn)
			}
		}
	}
}

func checkBinds(reg *Registry, rs *Resolver, rep *Report) {
	check := func(where, module string, binds []domain.Bind) {
		for _, b := range binds {
			if _, err := rs.ResolveBind(b, module); err != nil {
				rep.errorf("%s: bind %s = %s: %v", where, b.Slot, b.Target, err)
			}
		}
	}
	check(ProjectFile, "", reg.Root)
	for path, binds := range reg.FileBinds {
		check(filepath.Base(path), "", binds)
	}
	for _, mn := range reg.Names() {
		m := reg.Modules[mn]
		for path, binds := range m.FileBinds {
			check(mn+"/"+filepath.Base(path), mn, binds)
		}
	}
	each := func(wfs map[string]*domain.Workflow, module string) {
		for _, name := range sortedKeys(wfs) {
			wf := wfs[name]
			check(fmt.Sprintf("workflow %q", name), module, wf.Binds)
			for _, sn := range sortedKeys(wf.Steps) {
				check(fmt.Sprintf("workflow %q step %q", name, sn), module, wf.Steps[sn].Binds)
			}
		}
	}
	each(rs.Project, "")
	for _, mn := range reg.Names() {
		each(reg.Modules[mn].Workflows, mn)
	}
}

// checkWorkflow resolves each `workflow =` step through its static bind chain
// and checks contract compatibility.
func checkWorkflow(wf *domain.Workflow, rs *Resolver, reg *Registry, rep *Report) {
	label := wf.Name
	if wf.Module != "" {
		label = wf.Module + ":" + wf.Name
	}
	targets := map[string]*domain.Workflow{} // step name -> bound workflow
	for _, sn := range sortedKeys(wf.Steps) {
		step := wf.Steps[sn]
		slot := step.Config["workflow_name"]
		if step.Type != domain.StepTypeWorkflow || slot == "" {
			continue
		}
		stack := CallStack{reg.FrameFor(wf, step)}
		res, err := rs.ResolveSlot(stack, slot)
		if err != nil {
			if wf.Module != "" {
				rep.warnf("workflow %s step %q: slot %q is unbound (resolved by the caller's binds)", label, sn, slot)
			}
			// Project workflows with an undefined target are reported by the
			// cross-file reference check; a bad bind target by checkBinds.
			continue
		}
		targets[sn] = res.Workflow
		for _, r := range []string{"success", "fail"} {
			if !wired(wf, sn, r) {
				rep.errorf("workflow %s step %q: result %q of %q is not wired", label, sn, r, slot)
			}
		}
	}

	for _, sn := range sortedKeys(targets) {
		checkContract(wf, label, wf.Steps[sn], targets, rep)
	}
}

func wired(wf *domain.Workflow, step, result string) bool {
	for _, w := range wf.Wiring {
		if w.From == step && w.Result == result {
			return true
		}
	}
	return false
}

// upstream returns the steps that can run before step (reverse reachability).
func upstream(wf *domain.Workflow, step string) map[string]bool {
	preds := map[string][]string{}
	for _, w := range wf.Wiring {
		preds[w.To] = append(preds[w.To], w.From)
	}
	for _, c := range wf.Collects {
		for _, cond := range c.Conditions {
			preds[c.To] = append(preds[c.To], cond.Step)
		}
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(s string) {
		for _, p := range preds[s] {
			if !seen[p] {
				seen[p] = true
				walk(p)
			}
		}
	}
	walk(step)
	delete(seen, step)
	return seen
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func checkContract(wf *domain.Workflow, label string, step *domain.Step, targets map[string]*domain.Workflow, rep *Report) {
	child := targets[step.Name]
	slot := step.Config["workflow_name"]
	if child.Contract == nil {
		return
	}

	// Output remaps must name keys the child declares.
	for _, w := range step.With {
		if w.Source == domain.WithOut && !contains(child.Contract.Out, w.Value) {
			rep.errorf("workflow %s step %q: with reads out.%s but %s does not declare out:%s", label, step.Name, w.Value, child.Name, w.Value)
		}
	}

	avail := map[string]bool{}
	availEnv := map[string]bool{}
	for _, w := range step.With {
		if w.Source == domain.WithOut {
			continue
		}
		if w.Env {
			availEnv[w.Key] = true
		} else {
			avail[w.Key] = true
		}
	}
	if wf.Contract != nil {
		for _, k := range wf.Contract.KV {
			avail[k] = true
		}
		for _, k := range wf.Contract.Env {
			availEnv[k] = true
		}
	}
	untyped := false
	var noOut []string // upstream workflow steps that could have provided keys
	for up := range upstream(wf, step.Name) {
		us := wf.Steps[up]
		if us == nil {
			continue
		}
		t, isWF := targets[up]
		if us.Type != domain.StepTypeWorkflow || !isWF || t.Contract == nil {
			untyped = true
			continue
		}
		for _, k := range t.Contract.Out {
			avail[k] = true
		}
		for _, w := range us.With {
			if w.Source == domain.WithOut {
				avail[w.Key] = true
			}
		}
		noOut = append(noOut, up)
	}
	sort.Strings(noOut)

	for _, k := range child.Contract.KV {
		if avail[k] || seededKeys[k] || strings.HasPrefix(k, "cloche.") {
			continue
		}
		switch {
		case untyped:
			rep.warnf("workflow %s step %q: %s requires kv:%s, which may be written by an upstream script step (checked at runtime)", label, step.Name, slot, k)
		default:
			hint := ""
			if len(noOut) > 0 {
				hint = fmt.Sprintf("; upstream workflow step(s) %s do not declare out:%s", strings.Join(noOut, ", "), k)
			}
			rep.errorf("workflow %s step %q: %s requires kv:%s, which nothing provides%s", label, step.Name, slot, k, hint)
		}
	}
	for _, k := range child.Contract.Env {
		if !availEnv[k] {
			rep.warnf("workflow %s step %q: %s requires env:%s, which is not supplied by `with` or the caller's contract (checked at runtime)", label, step.Name, slot, k)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
