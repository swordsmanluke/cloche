package domain

// ResolveRunRepositories computes the set of [[repositories]] names a run
// belongs to, applying the attribution rules below in order and unioning
// their results — a run can belong to more than one repository, and the
// result is deduped and order-preserving. A nil result means "unattributed":
// the console shows the run only under "all repos", counted separately
// rather than silently folded into any one sub-tab.
//
// Rules, in order:
//
//	(a) explicit — the run's own Repository field, when the workflow declared
//	    exactly one repo at dispatch time (see SingleRepo).
//	(b) projectRepo — the repository whose configured path equals the run's
//	    project_dir, i.e. the run's project_dir was itself a sub-repo path
//	    (e.g. "cloche run" invoked from inside a sub-repo).
//	(c) touched — repositories independently observed to have been touched
//	    by the run: extraction/merge records for container sub-workflows, or
//	    the step "repository" pin for host workflow steps.
//	(d) wfRepos — every repository the workflow declares, used in full only
//	    when it declares more than one (a single declared repo is already
//	    captured by rule a).
func ResolveRunRepositories(explicit, projectRepo string, touched, wfRepos []string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	add(explicit)
	add(projectRepo)
	for _, t := range touched {
		add(t)
	}
	if len(wfRepos) > 1 {
		for _, r := range wfRepos {
			add(r)
		}
	}
	return out
}
