package intent

// RepoStats summarizes what one repository contributed to a single
// collect-sources pass: how much material was gathered and handed to the
// extract step. Name is "" for the project's own root directory, or a
// [[repositories]] entry's configured name for a descended-into repository.
type RepoStats struct {
	Name        string `json:"name" yaml:"name"`
	DocsNew     int    `json:"docs_new" yaml:"docs_new"`
	DocsChanged int    `json:"docs_changed" yaml:"docs_changed"`
	Commits     int    `json:"commits" yaml:"commits"`
	// CommitRange is the oldest..newest short SHA collected this pass, or ""
	// when Commits is 0.
	CommitRange string `json:"commit_range,omitempty" yaml:"commit_range,omitempty"`
	// Runs is the number of run directories (task prompts and/or
	// transcripts) collected this pass.
	Runs int `json:"runs" yaml:"runs"`
	// Bytes is the total size of everything this repo handed to extract
	// (doc content, commit patches, task prompts, transcripts).
	Bytes int64 `json:"bytes" yaml:"bytes"`
}

// Docs returns the total number of docs (new + changed) this repo
// contributed.
func (r RepoStats) Docs() int {
	return r.DocsNew + r.DocsChanged
}

// Empty reports whether this repo contributed nothing to the scan: no docs,
// commits, or runs. A configured (non-root) repo that comes back Empty is
// worth flagging to the user — it usually means a stale path or a repo that
// isn't actually a git checkout.
func (r RepoStats) Empty() bool {
	return r.Docs() == 0 && r.Commits == 0 && r.Runs == 0
}

// ScanStats summarizes one full intent-scan collect-sources pass across
// every repo it looked at: the project root plus any configured
// [[repositories]] entries.
type ScanStats struct {
	Repos []RepoStats `json:"repos,omitempty" yaml:"repos,omitempty"`
}

// TotalDocs, TotalCommits, TotalRuns, and TotalBytes sum the corresponding
// RepoStats field across every repo in the scan.
func (s ScanStats) TotalDocs() int {
	n := 0
	for _, r := range s.Repos {
		n += r.Docs()
	}
	return n
}

func (s ScanStats) TotalCommits() int {
	n := 0
	for _, r := range s.Repos {
		n += r.Commits
	}
	return n
}

func (s ScanStats) TotalRuns() int {
	n := 0
	for _, r := range s.Repos {
		n += r.Runs
	}
	return n
}

func (s ScanStats) TotalBytes() int64 {
	var n int64
	for _, r := range s.Repos {
		n += r.Bytes
	}
	return n
}

// EmptyRepoNames returns the names of every configured (non-root) repo that
// contributed nothing to the scan, for the Requirements view's warning.
func (s ScanStats) EmptyRepoNames() []string {
	var names []string
	for _, r := range s.Repos {
		if r.Name != "" && r.Empty() {
			names = append(names, r.Name)
		}
	}
	return names
}
