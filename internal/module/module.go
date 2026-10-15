// Package module discovers Cloche modules and loads them into a namespaced
// registry. Module workflows are reachable only through binds, so they are
// never merged into the project's workflow map.
package module

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dsl"
)

const (
	// ManifestFile is the module manifest's file name.
	ManifestFile = "module.toml"
	// LockFile is the lockfile's name, relative to .cloche/.
	LockFile = "modules.lock"
	// ProjectFile holds the project-root bind scope, relative to .cloche/.
	ProjectFile = "project.cloche"

	pathPrefix = "path:"
)

// Manifest is the parsed content of a module.toml.
type Manifest struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Exports     []string `toml:"exports"`
	Requires    []string `toml:"requires"`
	Credentials []string `toml:"credentials"`
	Uses        []string `toml:"uses"`
}

// LockEntry is one [[module]] table of modules.lock.
type LockEntry struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
	Ref    string `toml:"ref"`
	Commit string `toml:"commit"`
	Hash   string `toml:"hash"`
}

// LocalPath returns the path of a local-path source ("path:<dir>") and true,
// or "" and false for git sources.
func (e LockEntry) LocalPath() (string, bool) {
	if strings.HasPrefix(e.Source, pathPrefix) {
		return strings.TrimPrefix(e.Source, pathPrefix), true
	}
	return "", false
}

type lockfile struct {
	Modules []LockEntry `toml:"module"`
}

// Module is a loaded module.
type Module struct {
	Manifest
	Dir   string // absolute module root
	Local bool   // local-path module used in place, outside .cloche/modules/
	// Workflows holds every workflow in the module, exported or private,
	// keyed by name. Use Registry.Export to reach exported ones.
	Workflows map[string]*domain.Workflow
	// FileBinds are the top-level binds of each module .cloche file, keyed by
	// the file's path (matching Workflow.SourceFile).
	FileBinds map[string][]domain.Bind
}

// Exported reports whether name is listed in the module's exports.
func (m *Module) Exported(name string) bool {
	for _, e := range m.Exports {
		if e == name {
			return true
		}
	}
	return false
}

// Registry is the set of modules for a project plus the root bind scope.
type Registry struct {
	Modules map[string]*Module
	// Root holds the binds at the top level of .cloche/project.cloche.
	Root []domain.Bind
}

// Names returns the module names in sorted order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.Modules))
	for n := range r.Modules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Export returns the exported workflow wfName of module modName. Private
// workflows are not reachable from outside the module.
func (r *Registry) Export(modName, wfName string) (*domain.Workflow, error) {
	m, ok := r.Modules[modName]
	if !ok {
		return nil, fmt.Errorf("module %q is not installed", modName)
	}
	if !m.Exported(wfName) {
		return nil, fmt.Errorf("module %q does not export workflow %q", modName, wfName)
	}
	wf, ok := m.Workflows[wfName]
	if !ok {
		return nil, fmt.Errorf("module %q exports %q but defines no such workflow", modName, wfName)
	}
	return wf, nil
}

// Load builds the registry for projectDir: vendored modules from
// .cloche/modules/*/, local-path modules from .cloche/modules.lock, and the
// root bind scope from .cloche/project.cloche. A project with none of these
// yields an empty registry.
func Load(projectDir string) (*Registry, error) {
	clocheDir := filepath.Join(projectDir, ".cloche")
	reg := &Registry{Modules: make(map[string]*Module)}

	dirs, err := filepath.Glob(filepath.Join(clocheDir, "modules", "*", ManifestFile))
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	for _, manifest := range dirs {
		m, err := loadModule(filepath.Dir(manifest), false)
		if err != nil {
			return nil, err
		}
		if err := reg.add(m); err != nil {
			return nil, err
		}
	}

	entries, err := readLock(filepath.Join(clocheDir, LockFile))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		p, ok := e.LocalPath()
		if !ok {
			continue // git modules are vendored and found above
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectDir, p)
		}
		m, err := loadModule(filepath.Clean(p), true)
		if err != nil {
			return nil, fmt.Errorf("modules.lock entry %q: %w", e.Name, err)
		}
		if e.Name != "" && e.Name != m.Name {
			return nil, fmt.Errorf("modules.lock entry %q: %s declares name %q", e.Name, p, m.Name)
		}
		if err := reg.add(m); err != nil {
			return nil, err
		}
	}

	root, err := loadRoot(filepath.Join(clocheDir, ProjectFile))
	if err != nil {
		return nil, err
	}
	reg.Root = root
	return reg, nil
}

func (r *Registry) add(m *Module) error {
	if prev, dup := r.Modules[m.Name]; dup {
		return fmt.Errorf("duplicate module %q: %s and %s", m.Name, prev.Dir, m.Dir)
	}
	r.Modules[m.Name] = m
	return nil
}

func readLock(path string) ([]LockEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lf lockfile
	if err := toml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return lf.Modules, nil
}

func loadRoot(path string) ([]domain.Bind, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := dsl.ParseFile(string(data), dsl.WithSourceFile(path), dsl.WithAllowEmpty())
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f.Binds, nil
}

func loadModule(dir string, local bool) (*Module, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	mpath := filepath.Join(abs, ManifestFile)
	data, err := os.ReadFile(mpath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", mpath, err)
	}
	m := &Module{
		Dir:       abs,
		Local:     local,
		Workflows: make(map[string]*domain.Workflow),
		FileBinds: make(map[string][]domain.Bind),
	}
	if err := toml.Unmarshal(data, &m.Manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", mpath, err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("%s: name is required", mpath)
	}
	if !local && m.Name != filepath.Base(abs) {
		return nil, fmt.Errorf("%s: name %q does not match directory %q", mpath, m.Name, filepath.Base(abs))
	}

	files, err := filepath.Glob(filepath.Join(abs, "*.cloche"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := dsl.ParseFile(string(src), dsl.WithSourceFile(path), dsl.WithAllowEmpty())
		if err != nil {
			return nil, fmt.Errorf("module %q: parse %s: %w", m.Name, path, err)
		}
		if len(f.Binds) > 0 {
			m.FileBinds[path] = f.Binds
		}
		for name, wf := range f.Workflows {
			if prev, dup := m.Workflows[name]; dup {
				return nil, fmt.Errorf("module %q: duplicate workflow %q in %s and %s", m.Name, name, prev.SourceFile, path)
			}
			wf.Module = m.Name
			wf.ResolveAgents()
			m.Workflows[name] = wf
		}
	}
	return m, nil
}
