package scan

import (
	"fmt"
	"path"

	"github.com/swordsmanluke/cloche/internal/intent"
)

// RepoInput names one repository for CollectMulti to collect from. Dir is an
// absolute path. Name is "" for the project's own root directory, or a
// [[repositories]] entry's configured name for a descended-into repository.
type RepoInput struct {
	Name string
	Dir  string
}

// CollectMulti runs Collect against root and every entry in repos, merging
// the results into one Collection for the extract step to read and
// returning the ScanState to persist next.
//
// Docs and run material collected from a named (non-root) repo are
// namespaced under "repos/<name>/" so they can never collide with the
// root's own paths or run IDs when written out by Collection.Write; commit
// SHAs are already globally unique and need no such prefix. Each repo's own
// collection cursors advance independently (root's stay in prev's top-level
// fields, exactly as before repos existed; each configured repo's live
// under next.Repos[name]), so a repo that's added, removed, or temporarily
// unreadable doesn't disturb any other repo's progress.
func CollectMulti(root RepoInput, repos []RepoInput, prev *intent.ScanState, docGlobs []string, excludedSteps map[string]bool) (*Collection, *intent.ScanState, error) {
	if prev == nil {
		prev = &intent.ScanState{}
	}

	rootPrev := &intent.ScanState{LastCommit: prev.LastCommit, ScannedRuns: prev.ScannedRuns, ScannedDocs: prev.ScannedDocs}
	rootCollection, err := Collect(root.Dir, rootPrev, docGlobs, excludedSteps)
	if err != nil {
		return nil, nil, fmt.Errorf("collecting root: %w", err)
	}

	merged := &Collection{
		Docs:    append([]DocSource{}, rootCollection.Docs...),
		Commits: append([]CommitSource{}, rootCollection.Commits...),
		Runs:    append([]RunSource{}, rootCollection.Runs...),
	}
	stats := []intent.RepoStats{repoStats("", rootCollection, rootPrev.ScannedDocs)}

	next := rootCollection.NextState(rootPrev)
	next.Repos = map[string]*intent.RepoCursor{}
	for k, v := range prev.Repos {
		next.Repos[k] = v
	}

	for _, r := range repos {
		subPrev := &intent.ScanState{}
		if rc := prev.Repos[r.Name]; rc != nil {
			subPrev = &intent.ScanState{LastCommit: rc.LastCommit, ScannedRuns: rc.ScannedRuns, ScannedDocs: rc.ScannedDocs}
		}

		subCollection, err := Collect(r.Dir, subPrev, docGlobs, excludedSteps)
		if err != nil {
			return nil, nil, fmt.Errorf("collecting repo %q: %w", r.Name, err)
		}

		stats = append(stats, repoStats(r.Name, subCollection, subPrev.ScannedDocs))

		pref := namespaced(r.Name, subCollection)
		merged.Docs = append(merged.Docs, pref.Docs...)
		merged.Commits = append(merged.Commits, pref.Commits...)
		merged.Runs = append(merged.Runs, pref.Runs...)

		subNext := subCollection.NextState(subPrev)
		next.Repos[r.Name] = &intent.RepoCursor{
			LastCommit:  subNext.LastCommit,
			ScannedRuns: subNext.ScannedRuns,
			ScannedDocs: subNext.ScannedDocs,
		}
	}

	next.LastScanStats = intent.ScanStats{Repos: stats}
	return merged, next, nil
}

// namespaced returns a copy of c with every doc path and run ID prefixed by
// "repos/<name>/" so Collection.Write can lay a non-root repo's material out
// alongside the root's without collisions. A "" name (the root) is returned
// unchanged.
func namespaced(name string, c *Collection) *Collection {
	if name == "" {
		return c
	}
	out := &Collection{Commits: c.Commits}
	for _, d := range c.Docs {
		d.Path = path.Join("repos", name, d.Path)
		out.Docs = append(out.Docs, d)
	}
	for _, r := range c.Runs {
		r.ID = path.Join(name, r.ID)
		out.Runs = append(out.Runs, r)
	}
	return out
}

// repoStats builds the RepoStats summary for one repo's collection pass:
// docs classified new (unseen path) vs. changed (known path, new hash)
// against prevDocHashes, commit count and oldest..newest SHA range, run
// count, and total bytes handed to extract.
func repoStats(name string, c *Collection, prevDocHashes map[string]string) intent.RepoStats {
	var docsNew, docsChanged int
	var bytes int64
	for _, d := range c.Docs {
		if _, known := prevDocHashes[d.Path]; known {
			docsChanged++
		} else {
			docsNew++
		}
		bytes += int64(len(d.Content))
	}
	for _, cm := range c.Commits {
		bytes += int64(len(cm.Patch))
	}
	for _, r := range c.Runs {
		bytes += int64(len(r.TaskPrompt)) + int64(len(r.Transcript))
	}

	return intent.RepoStats{
		Name:        name,
		DocsNew:     docsNew,
		DocsChanged: docsChanged,
		Commits:     len(c.Commits),
		CommitRange: commitRange(c.Commits),
		Runs:        len(c.Runs),
		Bytes:       bytes,
	}
}

// commitRange formats the oldest..newest short SHA collected, or "" when
// commits is empty. git log (what collectCommits wraps) lists newest first,
// so commits[0] is the newest and commits[len-1] is the oldest.
func commitRange(commits []CommitSource) string {
	if len(commits) == 0 {
		return ""
	}
	newest := shortSHA(commits[0].SHA)
	oldest := shortSHA(commits[len(commits)-1].SHA)
	if oldest == newest {
		return oldest
	}
	return oldest + ".." + newest
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
