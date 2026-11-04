package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// rootSubsetPaths are the project-root paths a container gets when the root
// repo isn't part of its copy: the cloche project files its workflows run from,
// and the agent instructions that apply to the project.
var rootSubsetPaths = []string{".claude", ".cloche", ".clocheignore", "AGENTS.md", "CLAUDE.md"}

// RepoSelection is what a run's containers hold and which repos its results
// are extracted to, resolved from the run's requested repo names.
type RepoSelection struct {
	// Extract lists the repos that get result branches: the requested ones,
	// or every configured repo when none were requested.
	Extract []ResolvedRepo
	// Copy lists the repos copied into the container: Extract plus the
	// transitive depends_on of each.
	Copy []ResolvedRepo

	requested bool
	all       []ResolvedRepo
	legacy    bool
}

// SelectRepositories resolves requested repo names against [[repositories]].
// No names selects every configured repo. An unknown name or a broken
// depends_on graph is an error, so a typo can't turn into a full copy.
func (c *Config) SelectRepositories(projectDir string, names []string) (*RepoSelection, error) {
	all := c.ResolveRepositories(projectDir)
	if c == nil || len(c.Repositories) == 0 {
		if len(names) > 0 {
			return nil, fmt.Errorf("repos %s requested, but this project has no [[repositories]] configured", strings.Join(names, ", "))
		}
		return &RepoSelection{Extract: all, Copy: all, all: all, legacy: true}, nil
	}
	if len(names) == 0 {
		return &RepoSelection{Extract: all, Copy: all, all: all}, nil
	}
	if err := c.ValidateRepositoryDeps(); err != nil {
		return nil, err
	}

	byName := make(map[string]ResolvedRepo, len(all))
	deps := make(map[string][]string, len(c.Repositories))
	for _, r := range all {
		byName[r.Name] = r
	}
	for _, r := range c.Repositories {
		deps[r.Name] = r.DependsOn
	}

	sel := &RepoSelection{requested: true, all: all}
	inExtract := make(map[string]bool, len(names))
	for _, name := range names {
		r, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown repo %q; configured repos: %s", name, strings.Join(c.repositoryNames(), ", "))
		}
		if !inExtract[name] {
			inExtract[name] = true
			sel.Extract = append(sel.Extract, r)
		}
	}

	inCopy := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		if inCopy[name] {
			return
		}
		inCopy[name] = true
		sel.Copy = append(sel.Copy, byName[name])
		for _, d := range deps[name] {
			visit(d)
		}
	}
	for _, name := range names {
		visit(name)
	}
	return sel, nil
}

// ValidateRepositoryDeps reports a depends_on entry naming an unconfigured repo,
// or a dependency cycle.
func (c *Config) ValidateRepositoryDeps() error {
	if c == nil {
		return nil
	}
	deps := make(map[string][]string, len(c.Repositories))
	for _, r := range c.Repositories {
		deps[r.Name] = r.DependsOn
	}
	for _, r := range c.Repositories {
		for _, d := range r.DependsOn {
			if _, ok := deps[d]; !ok {
				return fmt.Errorf("repo %q depends_on unknown repo %q; configured repos: %s", r.Name, d, strings.Join(c.repositoryNames(), ", "))
			}
		}
	}

	const (
		visiting = 1
		done     = 2
	)
	state := make(map[string]int, len(deps))
	var walk func(name string, path []string) error
	walk = func(name string, path []string) error {
		path = append(path, name)
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("repo depends_on cycle: %s", strings.Join(path, " -> "))
		}
		state[name] = visiting
		for _, d := range deps[name] {
			if err := walk(d, path); err != nil {
				return err
			}
		}
		state[name] = done
		return nil
	}
	for _, r := range c.Repositories {
		if err := walk(r.Name, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) repositoryNames() []string {
	names := make([]string, 0, len(c.Repositories))
	for _, r := range c.Repositories {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	return names
}

// CopyNames returns the names of the repos in the copy, sorted, or nil when no
// repos were requested and the copy isn't narrowed by name.
func (s *RepoSelection) CopyNames() []string {
	if !s.requested {
		return nil
	}
	names := make([]string, 0, len(s.Copy))
	for _, r := range s.Copy {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	return names
}

// ExcludePaths returns the project-relative checkouts of configured repos left
// out of the copy.
func (s *RepoSelection) ExcludePaths() []string {
	in := make(map[string]bool, len(s.Copy))
	for _, r := range s.Copy {
		in[r.Name] = true
	}
	var out []string
	for _, r := range s.all {
		if isNestedRepo(r) && !in[r.Name] {
			out = append(out, r.SubPath)
		}
	}
	sort.Strings(out)
	return out
}

// CopyOnly returns the project-relative paths a copy is limited to when the
// root repo isn't part of it, or nil when the whole root is copied. A project
// that doesn't list its root in [[repositories]] never gets all of it.
func (s *RepoSelection) CopyOnly() []string {
	if s.legacy {
		return nil
	}
	for _, r := range s.Copy {
		if r.SubPath == "" {
			return nil
		}
	}
	out := append([]string(nil), rootSubsetPaths...)
	for _, r := range s.Copy {
		if isNestedRepo(r) {
			out = append(out, r.SubPath)
		}
	}
	sort.Strings(out)
	return out
}

// isNestedRepo reports whether r's checkout sits inside the project root, and
// so is part of the workspace copy. Repos elsewhere are never copied.
func isNestedRepo(r ResolvedRepo) bool {
	return r.SubPath != "" && !filepath.IsAbs(r.SubPath) && !strings.HasPrefix(r.SubPath, "..")
}
