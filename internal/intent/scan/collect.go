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

	"github.com/cloche-dev/cloche/internal/intent"
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
	Path    string // project-relative
	Content string
	Hash    string // sha256 hex of Content
}

// CommitSource is one non-noise commit since the last scan's LastCommit cursor.
type CommitSource struct {
	SHA     string
	Subject string
	Patch   string
}

// RunSource is the task prompt and/or transcript material for one run
// directory not yet covered by the last scan's ScannedRuns cursor.
type RunSource struct {
	ID         string // .cloche/runs/<id> directory name
	TaskPrompt string
	Transcript string
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

	// headCommit is the resolved HEAD sha at collection time (empty if the
	// project isn't a git repo or has no commits). The cursor always
	// advances to it, independent of how many commits were kept after
	// noise filtering, so noise-only ranges don't get re-walked forever.
	headCommit string
	// visitedRuns are every run directory examined, including ones with no
	// task prompt or transcript content, so an empty run directory isn't
	// re-examined on every scan.
	visitedRuns []string
}

// HasNew reports whether Collect found anything worth handing to the extract
// step. A scan with no new material emits the workflow's "none" result.
func (c *Collection) HasNew() bool {
	return len(c.Docs) > 0 || len(c.Commits) > 0 || len(c.Runs) > 0
}

// NextState returns the ScanState to persist after a scan that collected c,
// advancing prev's cursors. prev is never mutated.
func (c *Collection) NextState(prev *intent.ScanState) *intent.ScanState {
	if prev == nil {
		prev = &intent.ScanState{}
	}
	next := &intent.ScanState{
		LastCommit:  prev.LastCommit,
		ScannedRuns: append([]string{}, prev.ScannedRuns...),
		ScannedDocs: map[string]string{},
	}
	for k, v := range prev.ScannedDocs {
		next.ScannedDocs[k] = v
	}
	for _, d := range c.Docs {
		next.ScannedDocs[d.Path] = d.Hash
	}
	if c.headCommit != "" {
		next.LastCommit = c.headCommit
	}
	next.ScannedRuns = append(next.ScannedRuns, c.visitedRuns...)
	return next
}

// Collect gathers material changed since prev's cursors: docs matching
// docGlobs (DefaultDocGlobs when empty) whose content hash changed, commits
// since prev.LastCommit with per-task version-bump noise filtered out, and
// run directories under .cloche/runs/ not yet listed in prev.ScannedRuns.
func Collect(projectDir string, prev *intent.ScanState, docGlobs []string) (*Collection, error) {
	if prev == nil {
		prev = &intent.ScanState{}
	}
	if len(docGlobs) == 0 {
		docGlobs = DefaultDocGlobs
	}

	docs, err := collectDocs(projectDir, prev.ScannedDocs, docGlobs)
	if err != nil {
		return nil, fmt.Errorf("intent scan: collecting docs: %w", err)
	}

	commits, head, err := collectCommits(projectDir, prev.LastCommit)
	if err != nil {
		return nil, fmt.Errorf("intent scan: collecting commits: %w", err)
	}

	runs, visited, err := collectRuns(projectDir, prev.ScannedRuns)
	if err != nil {
		return nil, fmt.Errorf("intent scan: collecting runs: %w", err)
	}

	return &Collection{
		Docs:        docs,
		Commits:     commits,
		Runs:        runs,
		headCommit:  head,
		visitedRuns: visited,
	}, nil
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

func collectRuns(projectDir string, prevScanned []string) ([]RunSource, []string, error) {
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
		if transcript, err := collectTranscript(filepath.Join(projectDir, ".cloche", "logs", e.Name())); err == nil {
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
// token budget.
func collectTranscript(taskLogDir string) (string, error) {
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
				if err := walk(path); err != nil {
					return err
				}
				continue
			}
			if !strings.HasSuffix(item.Name(), ".log") {
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

// Write lays out the collected material under outDir for the extract step
// to read: outDir/docs/<path>, outDir/commits.txt + outDir/diffs/<sha>.patch,
// outDir/runs/<id>/{task_prompt.md,transcript.log}, and a manifest.json
// summarizing what's present.
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
			fmt.Fprintf(&commitsTxt, "%s\t%s\n", cm.SHA, cm.Subject)
			short := cm.SHA
			if len(short) > 7 {
				short = short[:7]
			}
			if err := os.WriteFile(filepath.Join(diffsDir, short+".patch"), []byte(cm.Patch), 0o644); err != nil {
				return err
			}
			manifest.Commits = append(manifest.Commits, cm.SHA)
		}
		if err := os.WriteFile(filepath.Join(outDir, "commits.txt"), []byte(commitsTxt.String()), 0o644); err != nil {
			return err
		}
	}

	for _, r := range c.Runs {
		dir := filepath.Join(outDir, "runs", r.ID)
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
		manifest.Runs = append(manifest.Runs, r.ID)
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
