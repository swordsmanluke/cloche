// Package scan implements the deterministic and validation logic behind the
// intent-scan workflow (see docs/plans/2026-09-13-intent-continuity-design.md,
// "Extraction: the intent-scan workflow"): gathering material to mine
// (Collect), and applying an agent's reconcile decisions to the intent.Store
// under the hard rules a scan must never break (Apply).
package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/intent"
)

// DefaultDocGlobs are the doc paths mined when a project doesn't configure
// intent.doc_globs of its own.
var DefaultDocGlobs = []string{
	"CLAUDE.md",
	"README*.md",
	"docs/**/*.md",
	".cloche/prompts/**/*.md",
}

// noiseCommitRe matches commit subjects that carry no extractable intent:
// per-task version bumps. Mirrors changelog-collect-commits.sh's filter.
var noiseCommitRe = regexp.MustCompile(`^Version \d+\.\d+\.\d+$`)

// DocSource is one changed-or-new doc file since the last scan.
type DocSource struct {
	// Repo is the SubPath of the repository this doc came from ("" for the
	// project root itself, or any legacy project with no [[repositories]]
	// configured).
	Repo string
	// Path is project-root-relative, already qualified by Repo when Repo is
	// set (e.g. "repos/anarkana/docs/x.md") — this is exactly the
	// provenance ref an extract-step candidate should record for a doc.
	Path    string
	Content string
	Hash    string // sha256 hex of Content
}

// CommitSource is one non-noise commit since the last scan's LastCommit cursor.
type CommitSource struct {
	// Repo is the SubPath of the repository this commit belongs to ("" for
	// the project root itself, or any legacy project with no
	// [[repositories]] configured).
	Repo    string
	SHA     string
	Subject string
	Patch   string
}

// Ref renders c's provenance-ready commit identifier: the bare SHA at the
// project root / a legacy project, or "<repo-subpath>@<sha>" for a
// configured repository.
func (c CommitSource) Ref() string {
	if c.Repo == "" {
		return c.SHA
	}
	return c.Repo + "@" + c.SHA
}

// RunSource is the task prompt and/or transcript material for one run
// directory not yet covered by the last scan's ScannedRuns cursor.
type RunSource struct {
	// Repo is the SubPath of the repository this run belongs to ("" for the
	// project root itself, or any legacy project with no [[repositories]]
	// configured).
	Repo       string
	ID         string // .cloche/runs/<id> directory name, unqualified
	TaskPrompt string
	Transcript string
}

// Ref renders r's provenance-ready run identifier: the bare run ID at the
// project root / a legacy project, or "<repo-subpath>/<run-id>" for a
// configured repository.
func (r RunSource) Ref() string {
	if r.Repo == "" {
		return r.ID
	}
	return r.Repo + "/" + r.ID
}

// maxTranscriptBytes caps how much log content is pulled in per run so a
// long-running step doesn't blow out the extract step's token budget.
const maxTranscriptBytes = 64 * 1024

// Collection is the material gathered by Collect, ready to be written for
// the extract step to read and to have its cursors persisted via NextState.
type Collection struct {
	Docs    []DocSource
	Commits []CommitSource
	Runs    []RunSource

	// nextRepos holds the advanced RepoScanState for every source Collect
	// walked, keyed by repo name ("" for the project root). NextState
	// overlays this onto prev's cursors for repos Collect didn't touch.
	nextRepos map[string]*intent.RepoScanState

	// Stats records what this pass collected, per repo (root plus every
	// configured [[repositories]] entry), for callers that persist it as
	// ScanState.LastScanStats and report it (collect-sources' per-repo log
	// lines, `cloche intent scan`'s summary, the Requirements view's meta
	// line).
	Stats intent.ScanStats
}

// HasNew reports whether Collect found anything worth handing to the extract
// step. A scan with no new material emits the workflow's "none" result.
func (c *Collection) HasNew() bool {
	return len(c.Docs) > 0 || len(c.Commits) > 0 || len(c.Runs) > 0
}

// NextState returns the ScanState to persist after a scan that collected c,
// advancing prev's per-repo cursors. prev is never mutated.
func (c *Collection) NextState(prev *intent.ScanState) *intent.ScanState {
	if prev == nil {
		prev = &intent.ScanState{}
	}
	next := &intent.ScanState{
		Repos:      map[string]*intent.RepoScanState{},
		LastScanAt: prev.LastScanAt,
	}
	for name, rs := range prev.Repos {
		next.Repos[name] = rs
	}
	for name, rs := range c.nextRepos {
		next.Repos[name] = rs
	}
	return next
}

// RepoSource is one source tree Collect walks: the project root itself, or
// one repository declared via [[repositories]].
type RepoSource struct {
	// Name is the repo's [[repositories]] name ("" for the project root).
	Name string
	// Path is the absolute filesystem path to the source tree.
	Path string
	// SubPath is Path's location relative to the project root ("" for the
	// project root itself). Used to qualify provenance refs and on-disk
	// output paths so multiple repos' material never collides.
	SubPath string
}

// repoSources returns the project root plus every repository configured via
// cfg's [[repositories]] entries. The project root is always included: even
// a thin orchestration wrapper's own docs, commits, and runs may carry
// intent, so a project with configured repositories is scanned as root +
// repos, never repos instead of root. A nil cfg or one with no configured
// repositories returns just the project root, matching pre-multi-repo
// behavior exactly.
func repoSources(projectDir string, cfg *config.Config) []RepoSource {
	sources := []RepoSource{{Path: projectDir}}
	if cfg == nil || len(cfg.Repositories) == 0 {
		return sources
	}
	for _, r := range cfg.ResolveRepositories(projectDir) {
		sources = append(sources, RepoSource{Name: r.Name, Path: r.Path, SubPath: r.SubPath})
	}
	return sources
}

// Collect gathers material changed since prev's cursors, across the project
// root and every repository configured via cfg's [[repositories]] (see
// repoSources): docs matching docGlobs (DefaultDocGlobs when empty) whose
// content hash changed, commits since each source's own LastCommit cursor
// with per-task version-bump noise filtered out, and run directories under
// each source's .cloche/runs/ not yet listed in its ScannedRuns cursor.
// excludedSteps names steps configured with `intent_tracking = false`:
// their log files are left out of each run's mined Transcript so
// noise/sensitive step output never seeds requirements. cfg may be nil, in
// which case only the project root is scanned, exactly as before
// [[repositories]] existed.
func Collect(projectDir string, cfg *config.Config, prev *intent.ScanState, docGlobs []string, excludedSteps map[string]bool) (*Collection, error) {
	if prev == nil {
		prev = &intent.ScanState{}
	}
	if len(docGlobs) == 0 {
		docGlobs = DefaultDocGlobs
	}

	coll := &Collection{nextRepos: map[string]*intent.RepoScanState{}}

	for _, src := range repoSources(projectDir, cfg) {
		repoPrev := prev.Repos[src.Name]
		if repoPrev == nil {
			repoPrev = &intent.RepoScanState{}
		}

		docs, err := collectDocs(src.Path, repoPrev.ScannedDocs, docGlobs)
		if err != nil {
			return nil, fmt.Errorf("intent scan: collecting docs for %s: %w", repoLabel(src), err)
		}

		commits, head, err := collectCommits(src.Path, repoPrev.LastCommit)
		if err != nil {
			return nil, fmt.Errorf("intent scan: collecting commits for %s: %w", repoLabel(src), err)
		}

		runs, visited, err := collectRuns(src.Path, repoPrev.ScannedRuns, excludedSteps)
		if err != nil {
			return nil, fmt.Errorf("intent scan: collecting runs for %s: %w", repoLabel(src), err)
		}

		nextDocs := map[string]string{}
		for k, v := range repoPrev.ScannedDocs {
			nextDocs[k] = v
		}
		for _, d := range docs {
			nextDocs[d.Path] = d.Hash
			coll.Docs = append(coll.Docs, DocSource{
				Repo:    src.SubPath,
				Path:    qualifyPath(src.SubPath, d.Path),
				Content: d.Content,
				Hash:    d.Hash,
			})
		}

		for _, cm := range commits {
			coll.Commits = append(coll.Commits, CommitSource{
				Repo:    src.SubPath,
				SHA:     cm.SHA,
				Subject: cm.Subject,
				Patch:   cm.Patch,
			})
		}

		for _, r := range runs {
			coll.Runs = append(coll.Runs, RunSource{
				Repo:       src.SubPath,
				ID:         r.ID,
				TaskPrompt: r.TaskPrompt,
				Transcript: r.Transcript,
			})
		}

		nextCommit := repoPrev.LastCommit
		if head != "" {
			nextCommit = head
		}
		coll.nextRepos[src.Name] = &intent.RepoScanState{
			LastCommit:  nextCommit,
			ScannedRuns: append(append([]string{}, repoPrev.ScannedRuns...), visited...),
			ScannedDocs: nextDocs,
		}

		coll.Stats.Repos = append(coll.Stats.Repos, repoStatsFor(src.Name, docs, commits, runs, repoPrev.ScannedDocs))
	}

	return coll, nil
}

// repoLabel names src for an error message: "the project root" for the
// project's own tree, or its configured repo name otherwise.
func repoLabel(src RepoSource) string {
	if src.Name == "" {
		return "the project root"
	}
	return src.Name
}

// repoStatsFor builds the RepoStats summary for one repo source's collection
// pass: docs classified new (unseen path) vs. changed (known path, new
// hash) against prevDocHashes, commit count and oldest..newest SHA range,
// run count, and total bytes handed to extract.
func repoStatsFor(name string, docs []DocSource, commits []CommitSource, runs []RunSource, prevDocHashes map[string]string) intent.RepoStats {
	var docsNew, docsChanged int
	var bytes int64
	for _, d := range docs {
		if _, known := prevDocHashes[d.Path]; known {
			docsChanged++
		} else {
			docsNew++
		}
		bytes += int64(len(d.Content))
	}
	for _, cm := range commits {
		bytes += int64(len(cm.Patch))
	}
	for _, r := range runs {
		bytes += int64(len(r.TaskPrompt)) + int64(len(r.Transcript))
	}

	return intent.RepoStats{
		Name:        name,
		DocsNew:     docsNew,
		DocsChanged: docsChanged,
		Commits:     len(commits),
		CommitRange: commitRange(commits),
		Runs:        len(runs),
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

// qualifyPath prefixes a repo-root-relative logical path (a doc path, a run
// ID) with its repo's location so multi-repo material never collides and so
// provenance refs disambiguate which repo they came from. Project-root
// material (subPath == "") is returned unprefixed, keeping single-repo
// projects' refs exactly as before [[repositories]] existed.
func qualifyPath(subPath, rel string) string {
	if subPath == "" {
		return rel
	}
	return subPath + "/" + rel
}

func collectDocs(projectDir string, prevHashes map[string]string, docGlobs []string) ([]DocSource, error) {
	paths, err := expandDocGlobs(projectDir, docGlobs)
	if err != nil {
		return nil, err
	}

	var docs []DocSource
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(projectDir, rel))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", rel, err)
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if prevHashes[rel] == hash {
			continue
		}
		docs = append(docs, DocSource{Path: rel, Content: string(data), Hash: hash})
	}
	return docs, nil
}

// expandDocGlobs resolves docGlobs to a sorted, deduplicated list of
// project-relative file paths. Patterns containing "/**/ " are matched by
// walking the directory tree rooted at the prefix before "/**/" and glob-
// matching the suffix against each file's base name; other patterns are
// resolved with filepath.Glob.
func expandDocGlobs(projectDir string, docGlobs []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	add := func(rel string) {
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}

	for _, pat := range docGlobs {
		if strings.Contains(pat, "**") {
			parts := strings.SplitN(pat, "/**/", 2)
			if len(parts) != 2 {
				continue
			}
			base := filepath.Join(projectDir, parts[0])
			suffix := parts[1]
			walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						return nil
					}
					return err
				}
				if d.IsDir() {
					return nil
				}
				ok, matchErr := filepath.Match(suffix, d.Name())
				if matchErr != nil {
					return matchErr
				}
				if ok {
					rel, err := filepath.Rel(projectDir, path)
					if err != nil {
						return err
					}
					add(filepath.ToSlash(rel))
				}
				return nil
			})
			if walkErr != nil {
				return nil, walkErr
			}
			continue
		}

		matches, err := filepath.Glob(filepath.Join(projectDir, pat))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(projectDir, m)
			if err != nil {
				return nil, err
			}
			add(filepath.ToSlash(rel))
		}
	}

	sort.Strings(out)
	return out, nil
}

func collectCommits(projectDir, lastCommit string) ([]CommitSource, string, error) {
	if _, err := os.Stat(filepath.Join(projectDir, ".git")); err != nil {
		return nil, "", nil
	}

	head, err := runGit(projectDir, "rev-parse", "HEAD")
	if err != nil {
		// No commits yet, or not a git repo in a usable state; not fatal.
		return nil, "", nil //nolint:nilerr
	}
	head = strings.TrimSpace(head)

	rangeSpec := "HEAD"
	if lastCommit != "" {
		if _, err := runGit(projectDir, "rev-parse", "--verify", lastCommit); err != nil {
			// Cursor points at a commit no longer reachable (e.g. history
			// rewrite); fall back to a full re-scan rather than failing.
			rangeSpec = "HEAD"
		} else if lastCommit == head {
			return nil, head, nil
		} else {
			rangeSpec = lastCommit + "..HEAD"
		}
	}

	raw, err := runGit(projectDir, "log", "--pretty=format:%H%x09%s", rangeSpec)
	if err != nil {
		return nil, "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, head, nil
	}

	var commits []CommitSource
	for _, line := range strings.Split(raw, "\n") {
		sha, subject, ok := strings.Cut(line, "\t")
		if !ok || sha == "" {
			continue
		}
		if noiseCommitRe.MatchString(subject) {
			continue
		}
		patch, err := runGit(projectDir, "show", "--stat", "--patch", sha)
		if err != nil {
			return nil, "", err
		}
		commits = append(commits, CommitSource{SHA: sha, Subject: subject, Patch: patch})
	}
	return commits, head, nil
}

func runGit(projectDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", projectDir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, ee.Stderr)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func collectRuns(projectDir string, prevScanned []string, excludedSteps map[string]bool) ([]RunSource, []string, error) {
	already := map[string]bool{}
	for _, id := range prevScanned {
		already[id] = true
	}

	runsDir := filepath.Join(projectDir, ".cloche", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("reading %s: %w", runsDir, err)
	}

	var runs []RunSource
	var visited []string
	for _, e := range entries {
		if !e.IsDir() || already[e.Name()] {
			continue
		}
		visited = append(visited, e.Name())

		src := RunSource{ID: e.Name()}
		if data, err := os.ReadFile(filepath.Join(runsDir, e.Name(), "task_prompt.md")); err == nil {
			src.TaskPrompt = string(data)
		}
		if transcript, err := collectTranscript(filepath.Join(projectDir, ".cloche", "logs", e.Name()), excludedSteps); err == nil {
			src.Transcript = transcript
		}
		if src.TaskPrompt != "" || src.Transcript != "" {
			runs = append(runs, src)
		}
	}

	sort.Strings(visited)
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	return runs, visited, nil
}

// collectTranscript concatenates every *.log file under a task's
// .cloche/logs/<task-id>/ tree (step output from every attempt), capped to
// maxTranscriptBytes so one verbose run can't dominate the extract step's
// token budget. excludedSteps names steps opted out of mining via
// `intent_tracking = false`: their log files are skipped, and a
// sub-workflow directory named after an excluded step is skipped entirely.
func collectTranscript(taskLogDir string, excludedSteps map[string]bool) (string, error) {
	if _, err := os.Stat(taskLogDir); err != nil {
		return "", err
	}

	var b strings.Builder
	var walk func(dir string) error
	walk = func(dir string) error {
		items, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
		for _, item := range items {
			path := filepath.Join(dir, item.Name())
			if item.IsDir() {
				if excludedSteps[item.Name()] {
					continue
				}
				if err := walk(path); err != nil {
					return err
				}
				continue
			}
			if !strings.HasSuffix(item.Name(), ".log") {
				continue
			}
			if excludedSteps[transcriptLogStepName(item.Name())] {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if b.Len() >= maxTranscriptBytes {
				continue
			}
			b.WriteString(string(data))
			b.WriteString("\n")
		}
		return nil
	}
	if err := walk(taskLogDir); err != nil {
		return "", err
	}

	out := b.String()
	if len(out) > maxTranscriptBytes {
		out = out[:maxTranscriptBytes]
	}
	return out, nil
}

// transcriptLogStepName derives the step name a log file belongs to from
// its filename, per the "<step>.log" / "llm-<step>.log" conventions used
// under .cloche/logs/ (see indexLogFiles in internal/adapters/grpc/server.go).
func transcriptLogStepName(fileName string) string {
	base := strings.TrimSuffix(fileName, ".log")
	return strings.TrimPrefix(base, "llm-")
}

// Write lays out the collected material under outDir for the extract step
// to read: outDir/docs/<path> (already repo-qualified for a non-root
// source), outDir/commits.txt (one repo-qualified ref per line, see
// CommitSource.Ref) + outDir/diffs/<sha-or-repo-sha>.patch,
// outDir/runs/<ref>/{task_prompt.md,transcript.log} (ref per
// RunSource.Ref), and a manifest.json summarizing what's present.
func (c *Collection) Write(outDir string) error {
	manifest := struct {
		Docs    []string `json:"docs"`
		Commits []string `json:"commits"`
		Runs    []string `json:"runs"`
	}{}

	for _, d := range c.Docs {
		path := filepath.Join(outDir, "docs", filepath.FromSlash(d.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(d.Content), 0o644); err != nil {
			return err
		}
		manifest.Docs = append(manifest.Docs, d.Path)
	}

	if len(c.Commits) > 0 {
		diffsDir := filepath.Join(outDir, "diffs")
		if err := os.MkdirAll(diffsDir, 0o755); err != nil {
			return err
		}
		var commitsTxt strings.Builder
		for _, cm := range c.Commits {
			ref := cm.Ref()
			fmt.Fprintf(&commitsTxt, "%s\t%s\n", ref, cm.Subject)
			short := cm.SHA
			if len(short) > 7 {
				short = short[:7]
			}
			// Prefix the patch filename with the repo when set, so commits
			// from different repos that happen to share a short SHA prefix
			// can't collide.
			base := short
			if cm.Repo != "" {
				base = strings.ReplaceAll(cm.Repo, "/", "-") + "-" + short
			}
			if err := os.WriteFile(filepath.Join(diffsDir, base+".patch"), []byte(cm.Patch), 0o644); err != nil {
				return err
			}
			manifest.Commits = append(manifest.Commits, ref)
		}
		if err := os.WriteFile(filepath.Join(outDir, "commits.txt"), []byte(commitsTxt.String()), 0o644); err != nil {
			return err
		}
	}

	for _, r := range c.Runs {
		ref := r.Ref()
		dir := filepath.Join(outDir, "runs", filepath.FromSlash(ref))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if r.TaskPrompt != "" {
			if err := os.WriteFile(filepath.Join(dir, "task_prompt.md"), []byte(r.TaskPrompt), 0o644); err != nil {
				return err
			}
		}
		if r.Transcript != "" {
			if err := os.WriteFile(filepath.Join(dir, "transcript.log"), []byte(r.Transcript), 0o644); err != nil {
				return err
			}
		}
		manifest.Runs = append(manifest.Runs, ref)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "manifest.json"), data, 0o644)
}
