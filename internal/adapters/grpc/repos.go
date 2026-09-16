package grpc

import (
	"fmt"
	"sort"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/domain"
)

// resolvedRepo describes one repository the daemon should extract changes into
// for a given workflow run. Alias of config.ResolvedRepo, the shared
// repo-resolution type also used by the intent-scan collector.
type resolvedRepo = config.ResolvedRepo

// resolveRepos returns the repos a workflow should produce extract branches
// for. If the workflow declares `repos = [...]`, those names are looked up in
// cfg.Repositories. Otherwise every [[repositories]] entry is used. When no
// repositories are configured at all, returns a single legacy entry pointing
// at projectDir so existing single-tree projects keep working.
func resolveRepos(wf *domain.Workflow, cfg *config.Config, projectDir string) ([]resolvedRepo, error) {
	all := cfg.ResolveRepositories(projectDir)
	if cfg == nil || len(cfg.Repositories) == 0 || wf == nil || len(wf.Repos) == 0 {
		return all, nil
	}

	byName := make(map[string]resolvedRepo, len(all))
	for _, r := range all {
		byName[r.Name] = r
	}

	out := make([]resolvedRepo, 0, len(wf.Repos))
	for _, name := range wf.Repos {
		r, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("workflow %q: declared repo %q not in [[repositories]] config", wf.Name, name)
		}
		out = append(out, r)
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
