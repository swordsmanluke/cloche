package module

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// ModuleDirEnv is the environment variable every `run` command of a module
// workflow receives. It is the module's host path on the host and its
// /workspace path inside a container.
const ModuleDirEnv = "CLOCHE_MODULE_DIR"

const containerWorkspace = "/workspace"

// ModuleFor returns the module that provides wf, or nil for project
// workflows and unknown modules. Safe on a nil registry.
func (r *Registry) ModuleFor(wf *domain.Workflow) *Module {
	if r == nil || wf == nil || wf.Module == "" {
		return nil
	}
	return r.Modules[wf.Module]
}

// RelPath returns the module root's path relative to projectDir (slash
// separated) and true when the module lives inside the project tree.
func (m *Module) RelPath(projectDir string) (string, bool) {
	rel, err := filepath.Rel(projectDir, m.Dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// External reports whether the module is a local-path module outside the
// project tree, which a container only sees if it is copied in.
func (m *Module) External(projectDir string) bool {
	if !m.Local {
		return false
	}
	_, inside := m.RelPath(projectDir)
	return !inside
}

// ContainerRel is the module root's path relative to /workspace inside a
// container: its project-relative path when it lives in the project, and
// .cloche/modules/<name> for an external local-path module (copied in there).
func (m *Module) ContainerRel(projectDir string) string {
	if rel, ok := m.RelPath(projectDir); ok {
		return rel
	}
	return ".cloche/modules/" + m.Name
}

// ContainerDir is the module root as seen inside a container.
func (m *Module) ContainerDir(projectDir string) string {
	return containerWorkspace + "/" + m.ContainerRel(projectDir)
}

// ExternalModules returns the local-path modules outside projectDir, sorted
// by name.
func (r *Registry) ExternalModules(projectDir string) []*Module {
	if r == nil {
		return nil
	}
	var out []*Module
	for _, n := range r.Names() {
		if m := r.Modules[n]; m.External(projectDir) {
			out = append(out, m)
		}
	}
	return out
}

// ResolveFileRef rewrites a `file("path")` config value of a module workflow
// so the path is relative to the module root instead of the project root. On
// the host (container=false) the result is an absolute path; in a container
// it is relative to /workspace. Values that are not file() references, or
// whose path is already absolute, are returned unchanged.
func (m *Module) ResolveFileRef(value, projectDir string, container bool) string {
	if !strings.HasPrefix(value, `file("`) || !strings.HasSuffix(value, `")`) {
		return value
	}
	path := value[len(`file("`) : len(value)-len(`")`)]
	if filepath.IsAbs(path) {
		return value
	}
	var resolved string
	if container {
		resolved = m.ContainerRel(projectDir) + "/" + filepath.ToSlash(path)
	} else {
		resolved = filepath.Join(m.Dir, path)
	}
	return `file("` + resolved + `")`
}

// SeedWith evaluates the input entries of a `with` block (everything except
// `from out.<key>` entries). get reads the caller's KV. Entries whose source
// KV key is unset are skipped so the child's contract check reports them.
func SeedWith(entries []domain.WithEntry, get func(key string) (string, bool)) (kv, env map[string]string) {
	kv = map[string]string{}
	env = map[string]string{}
	for _, e := range entries {
		var val string
		switch e.Source {
		case domain.WithLiteral:
			val = e.Value
		case domain.WithKV:
			v, ok := get(e.Value)
			if !ok {
				continue
			}
			val = v
		default:
			continue
		}
		if e.Env {
			env[e.Key] = val
		} else {
			kv[e.Key] = val
		}
	}
	return kv, env
}

// CheckInputs verifies a workflow's contract inputs before it starts. hasKV
// and hasEnv report whether a key or variable is set. It returns nil when
// the contract is satisfied or absent.
func CheckInputs(slot string, c *domain.Contract, hasKV, hasEnv func(string) bool) error {
	if c == nil {
		return nil
	}
	var missing []string
	for _, k := range c.KV {
		if !hasKV(k) {
			missing = append(missing, fmt.Sprintf("contract: %s requires kv:%s (not set)", slot, k))
		}
	}
	for _, k := range c.Env {
		if !hasEnv(k) {
			missing = append(missing, fmt.Sprintf("contract: %s requires env:%s (not set)", slot, k))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s", strings.Join(missing, "; "))
	}
	return nil
}

// CheckOutputs verifies a workflow that ended in done wrote every out: key.
func CheckOutputs(slot string, c *domain.Contract, hasKV func(string) bool) error {
	if c == nil {
		return nil
	}
	var missing []string
	for _, k := range c.Out {
		if !hasKV(k) {
			missing = append(missing, fmt.Sprintf("contract: %s must write out:%s (not set)", slot, k))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s", strings.Join(missing, "; "))
	}
	return nil
}

type envKey struct{}

// WithEnv returns ctx carrying extra environment variables for the steps of
// a child run, merged over any the context already carries.
func WithEnv(ctx context.Context, env map[string]string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	merged := map[string]string{}
	for k, v := range EnvFromContext(ctx) {
		merged[k] = v
	}
	for k, v := range env {
		merged[k] = v
	}
	return context.WithValue(ctx, envKey{}, merged)
}

// EnvFromContext returns the extra environment carried by ctx.
func EnvFromContext(ctx context.Context) map[string]string {
	m, _ := ctx.Value(envKey{}).(map[string]string)
	return m
}

// EnvList renders env as sorted KEY=VALUE entries.
func EnvList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// StepEnvPrefix prefixes step config keys that carry per-step environment
// variables into a container (the ExecuteStep wire format has no env field).
const StepEnvPrefix = "env."
