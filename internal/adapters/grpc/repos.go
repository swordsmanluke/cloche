package grpc

import (
	"fmt"
	"path/filepath"
	"sort"
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
		// Fresh slice: reslicing cfg.Repositories would overwrite its backing
		// array and corrupt the config for any later reader.
		chosen = make([]config.RepositoryConfig, 0, len(wf.Repos))
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

// reposForContainer returns the repository names a container's workspace copy
// must include: the union of `repos = [...]` declarations across every
// container workflow sharing containerID. Workflows with the same container.id
// reuse one container (and thus one workspace copy) within an attempt, so the
// copy has to satisfy all of them. Returns nil — meaning "no restriction,
// include everything" — when any sharing workflow declares no repos (it may
// depend on any of them) or when no matching workflow is found.
func reposForContainer(allWFs map[string]*domain.Workflow, containerID string) []string {
	seen := make(map[string]bool)
	var names []string
	found := false
	for _, wf := range allWFs {
		if wf == nil || wf.Location != domain.LocationContainer || wf.ContainerID() != containerID {
			continue
		}
		found = true
		if len(wf.Repos) == 0 {
			return nil
		}
		for _, n := range wf.Repos {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	if !found {
		return nil
	}
	sort.Strings(names)
	return names
}
