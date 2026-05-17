package grpc

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
)

// resolvedRepo describes one repository the daemon should extract changes into
// for a given workflow run.
type resolvedRepo struct {
	// Name is the [[repositories]] name from config.toml. Empty in legacy mode
	// (no repositories declared), where the project root itself is treated as
	// the single repo.
	Name string

	// Path is the absolute host path to the repo's working tree (where its
	// .git lives).
	Path string

	// SubPath is the repo's location inside the container workspace, relative
	// to /workspace. Empty for legacy mode (the whole /workspace is the repo).
	// Example: "repos/cloche" for a repo whose config path is "./repos/cloche".
	SubPath string
}

// resolveRepos returns the repos a workflow should produce extract branches
// for. If the workflow declares `repos = [...]`, those names are looked up in
// cfg.Repositories. Otherwise every [[repositories]] entry is used. When no
// repositories are configured at all, returns a single legacy entry pointing
// at projectDir so existing single-tree projects keep working.
func resolveRepos(wf *domain.Workflow, cfg *config.Config, projectDir string) ([]resolvedRepo, error) {
	if cfg == nil || len(cfg.Repositories) == 0 {
		return []resolvedRepo{{Path: projectDir}}, nil
	}

	byName := make(map[string]config.RepositoryConfig, len(cfg.Repositories))
	for _, r := range cfg.Repositories {
		byName[r.Name] = r
	}

	chosen := cfg.Repositories
	if wf != nil && len(wf.Repos) > 0 {
		chosen = chosen[:0]
		for _, name := range wf.Repos {
			r, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("workflow %q: declared repo %q not in [[repositories]] config", wf.Name, name)
			}
			chosen = append(chosen, r)
		}
	}

	out := make([]resolvedRepo, 0, len(chosen))
	for _, r := range chosen {
		abs := r.Path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(projectDir, r.Path)
		}
		sub := strings.TrimPrefix(filepath.Clean(r.Path), "./")
		if sub == "." {
			sub = ""
		}
		out = append(out, resolvedRepo{
			Name:    r.Name,
			Path:    abs,
			SubPath: sub,
		})
	}
	return out, nil
}
