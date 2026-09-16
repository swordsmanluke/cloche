package promptrev

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Commit is one entry of a prompt file's git history, newest first.
type Commit struct {
	SHA, Date, Message string
}

// cachedHistory is one HistoryCache entry: the commits found the last time
// History was computed, and the HEAD they were computed at.
type cachedHistory struct {
	head    string
	commits []Commit
}

// HistoryCache caches per-file `git log --follow` history keyed on the
// repo's current HEAD, so a request that hits the same file at an unchanged
// HEAD (the common case between commits) never shells out. Safe for
// concurrent use.
type HistoryCache struct {
	mu      sync.Mutex
	entries map[string]cachedHistory
}

// NewHistoryCache constructs an empty HistoryCache.
func NewHistoryCache() *HistoryCache {
	return &HistoryCache{entries: map[string]cachedHistory{}}
}

// History returns relPath's commit history within dir, newest first, or nil
// if dir isn't a git repo or the file has no history. The underlying `git
// log --follow` subprocess runs at most once per (dir, relPath) pair per
// HEAD; a cache hit costs one filesystem read (headSHA) and no subprocess.
func (c *HistoryCache) History(dir, relPath string) []Commit {
	head := headSHA(dir)
	key := dir + "\x00" + relPath

	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok && head != "" && cached.head == head {
		return cached.commits
	}

	commits := gitFollowHistory(dir, relPath)
	c.mu.Lock()
	c.entries[key] = cachedHistory{head: head, commits: commits}
	c.mu.Unlock()
	return commits
}

// headSHA resolves dir's current HEAD commit. It reads .git/HEAD and
// follows a symbolic ref straight to the loose ref file on disk, avoiding a
// git subprocess in the common case; it falls back to `git rev-parse HEAD`
// only for layouts a plain file read can't handle (packed refs, unusual
// .git layouts, not a git repo at all).
func headSHA(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return gitRevParseHead(dir)
	}

	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "ref:") {
		return content // detached HEAD: the file already holds the SHA
	}

	ref := strings.TrimSpace(strings.TrimPrefix(content, "ref:"))
	if refData, err := os.ReadFile(filepath.Join(gitDir, ref)); err == nil {
		return strings.TrimSpace(string(refData))
	}
	if sha := readPackedRef(gitDir, ref); sha != "" {
		return sha
	}
	return gitRevParseHead(dir)
}

// readPackedRef looks up ref in .git/packed-refs (written by `git gc` in
// place of a loose ref file), returning "" if the file or ref is absent.
func readPackedRef(gitDir, ref string) string {
	f, err := os.Open(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, " "+ref) {
			return strings.TrimSpace(strings.SplitN(line, " ", 2)[0])
		}
	}
	return ""
}

func gitRevParseHead(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitFollowHistory returns relPath's commit history within dir, newest
// first, or nil if dir isn't a git repo / the file has no history.
func gitFollowHistory(dir, relPath string) []Commit {
	cmd := exec.Command("git", "log", "--follow", "--format=%H%x1f%aI%x1f%s", "--", relPath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var history []Commit
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 3 {
			continue
		}
		date := parts[1]
		if t, err := time.Parse(time.RFC3339, date); err == nil {
			date = t.Format("2006-01-02")
		}
		history = append(history, Commit{SHA: parts[0], Date: date, Message: parts[2]})
	}
	return history
}
