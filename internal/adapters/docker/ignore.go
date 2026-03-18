package docker

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ignorePattern represents a single .clocheignore rule.
type ignorePattern struct {
	pattern   string // the glob pattern (cleaned)
	negated   bool   // starts with !
	dirOnly   bool   // trailing / means only match directories
	anchored  bool   // contains / (other than trailing) → anchored to root
	matchBase bool   // no / in pattern → match against basename in any dir
}

// parseClocheignore reads a .clocheignore file and returns the parsed patterns.
// Returns nil (no error) if the file does not exist.
func parseClocheignore(projectDir string) ([]ignorePattern, error) {
	path := filepath.Join(projectDir, ".clocheignore")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var patterns []ignorePattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p := ignorePattern{}

		if strings.HasPrefix(line, "!") {
			p.negated = true
			line = line[1:]
		}

		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimRight(line, "/")
		}

		// Leading "/" anchors to root; remove it for matching since
		// we'll match against paths relative to root.
		if strings.HasPrefix(line, "/") {
			p.anchored = true
			line = line[1:]
		}

		// If the pattern contains a slash (after stripping leading/trailing),
		// it's anchored. Otherwise it can match at any depth.
		if strings.Contains(line, "/") {
			p.anchored = true
		} else if !p.anchored {
			p.matchBase = true
		}

		p.pattern = line
		patterns = append(patterns, p)
	}
	return patterns, scanner.Err()
}

// isIgnored checks whether a relative path should be excluded.
// relPath should use forward slashes and not start with "./".
// isDir indicates whether the path is a directory.
func isIgnored(patterns []ignorePattern, relPath string, isDir bool) bool {
	if len(patterns) == 0 {
		return false
	}

	ignored := false
	for _, p := range patterns {
		if p.dirOnly && !isDir {
			continue
		}

		matched := false
		if p.matchBase {
			// Match against the final component only.
			base := filepath.Base(relPath)
			matched = matchGlob(p.pattern, base)
		} else if p.anchored {
			matched = matchGlob(p.pattern, relPath)
		} else {
			matched = matchGlob(p.pattern, relPath)
		}

		if matched {
			ignored = !p.negated
		}
	}
	return ignored
}

// matchGlob matches a gitignore-style glob against a path.
// Supports *, ?, and ** (matches any number of path components).
func matchGlob(pattern, name string) bool {
	// Handle ** patterns by expanding them.
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, name)
	}
	matched, _ := filepath.Match(pattern, name)
	return matched
}

// matchDoublestar handles patterns containing **.
func matchDoublestar(pattern, name string) bool {
	// Split pattern on "**" and handle recursively.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := parts[1]

	// Remove leading/trailing slashes from the boundary.
	prefix = strings.TrimSuffix(prefix, "/")
	suffix = strings.TrimPrefix(suffix, "/")

	if prefix == "" && suffix == "" {
		// Pattern is just "**" — matches everything.
		return true
	}

	if prefix == "" {
		// Pattern starts with "**/" — match suffix against any suffix of name.
		nameParts := strings.Split(name, "/")
		for i := range nameParts {
			candidate := strings.Join(nameParts[i:], "/")
			if matchGlob(suffix, candidate) {
				return true
			}
		}
		return false
	}

	if suffix == "" {
		// Pattern ends with "/**" — match prefix against any prefix of name.
		nameParts := strings.Split(name, "/")
		for i := 1; i <= len(nameParts); i++ {
			candidate := strings.Join(nameParts[:i], "/")
			if matchGlob(prefix, candidate) {
				return true
			}
		}
		return false
	}

	// Pattern has ** in the middle — try all splits of name.
	nameParts := strings.Split(name, "/")
	for i := 1; i < len(nameParts); i++ {
		left := strings.Join(nameParts[:i], "/")
		right := strings.Join(nameParts[i:], "/")
		if matchGlob(prefix, left) && matchGlob(suffix, right) {
			return true
		}
	}
	return false
}
