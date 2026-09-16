package config

import (
	"path/filepath"
	"strings"
)

// ResolvedRepo describes one repository configured via [[repositories]],
// with its path resolved against a project directory.
type ResolvedRepo struct {
	// Name is the [[repositories]] name from config.toml. Empty in legacy
	// mode (no repositories declared), where the project root itself is
	// treated as the single repo.
	Name string

	// Path is the absolute host path to the repo's working tree (where its
	// .git lives).
	Path string

	// SubPath is the repo's location relative to the project root. Empty for
	// legacy mode (the project root itself is the repo). Example:
	// "repos/cloche" for a repo whose config path is "./repos/cloche".
	SubPath string
}

// ResolveRepositories returns every repository declared via [[repositories]]
// in c, with paths resolved against projectDir. When c is nil or declares no
// repositories, returns a single legacy entry pointing at projectDir itself,
// so single-tree projects behave identically to before repos existed.
func (c *Config) ResolveRepositories(projectDir string) []ResolvedRepo {
	if c == nil || len(c.Repositories) == 0 {
		return []ResolvedRepo{{Path: projectDir}}
	}

	out := make([]ResolvedRepo, 0, len(c.Repositories))
	for _, r := range c.Repositories {
		abs := r.Path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(projectDir, r.Path)
		}
		sub := strings.TrimPrefix(filepath.Clean(r.Path), "./")
		if sub == "." {
			sub = ""
		}
		out = append(out, ResolvedRepo{
			Name:    r.Name,
			Path:    abs,
			SubPath: sub,
		})
	}
	return out
}
