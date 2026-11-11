// Package dra loads a project's Domain Responsible Agent definitions from
// .cloche/dras/<name>/. A DRA directory holds charter.md (required) and
// notes.md (its memory; created empty if missing). The name is the domain.
//
// Cloche never commits notes.md; git handling is left to the project.
package dra

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// DirName is the directory under .cloche/ that holds DRAs.
	DirName = "dras"
	// CharterFile is the required charter file.
	CharterFile = "charter.md"
	// NotesFile is the DRA's memory file.
	NotesFile = "notes.md"
)

var nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName reports whether name is a legal DRA name (same rule as a label:
// lowercase letters, digits and '-').
func ValidName(name string) bool { return nameRE.MatchString(name) }

// DRA is one defined agent.
type DRA struct {
	Name        string // the domain
	Dir         string
	CharterPath string
	NotesPath   string
	// Heading is the charter's first markdown heading ("" if none/unreadable).
	Heading string
	// Problems lists validation problems; empty when the DRA is valid.
	Problems []string
}

// Valid reports whether the DRA has no problems.
func (d DRA) Valid() bool { return len(d.Problems) == 0 }

// Root returns the dras directory for a project root.
func Root(projectDir string) string {
	return filepath.Join(projectDir, ".cloche", DirName)
}

// List returns the project's DRAs sorted by name. A missing dras directory
// yields none. Invalid DRAs are included with Problems set. It does not
// modify the filesystem.
func List(projectDir string) ([]DRA, error) {
	root := Root(projectDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []DRA
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		if !e.IsDir() {
			// Follow symlinks to directories; skip everything else.
			if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
				continue
			}
		}
		d := DRA{
			Name:        e.Name(),
			Dir:         path,
			CharterPath: filepath.Join(path, CharterFile),
			NotesPath:   filepath.Join(path, NotesFile),
		}
		if !ValidName(d.Name) {
			d.Problems = append(d.Problems, fmt.Sprintf("DRA %q: invalid name (use lowercase letters, digits and '-')", d.Name))
		}
		data, err := os.ReadFile(d.CharterPath)
		if err != nil {
			d.Problems = append(d.Problems, fmt.Sprintf("DRA %q: missing %s", d.Name, CharterFile))
		} else {
			d.Heading = FirstHeading(string(data))
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EnsureNotes creates an empty notes.md for the DRA if it is missing.
func EnsureNotes(d DRA) error {
	if _, err := os.Stat(d.NotesPath); err == nil {
		return nil
	}
	return os.WriteFile(d.NotesPath, nil, 0o644)
}

// Validate returns the problems of every DRA in the project.
func Validate(projectDir string) []string {
	ds, err := List(projectDir)
	if err != nil {
		return []string{fmt.Sprintf("reading %s: %v", Root(projectDir), err)}
	}
	var errs []string
	for _, d := range ds {
		errs = append(errs, d.Problems...)
	}
	return errs
}

// FirstHeading returns the text of the first markdown ATX heading, ignoring
// headings inside fenced code blocks.
func FirstHeading(md string) string {
	var fenceCh byte
	fenceLen := 0
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSuffix(line, "\r")
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent > 3 {
			continue
		}
		t := line[indent:]
		if n := fenceRun(t); n >= 3 {
			ch := t[0]
			if fenceLen == 0 {
				fenceCh, fenceLen = ch, n
				continue
			}
			if ch == fenceCh && n >= fenceLen && strings.TrimSpace(t[n:]) == "" {
				fenceLen = 0
			}
			continue
		}
		if fenceLen > 0 || !strings.HasPrefix(t, "#") {
			continue
		}
		n := len(t) - len(strings.TrimLeft(t, "#"))
		if n > 6 {
			continue
		}
		rest := t[n:]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		h := strings.TrimSpace(rest)
		// Strip an optional closing sequence of #s preceded by whitespace.
		if tr := strings.TrimRight(h, "#"); tr != h && (tr == "" || tr[len(tr)-1] == ' ' || tr[len(tr)-1] == '\t') {
			h = strings.TrimSpace(tr)
		}
		if h != "" {
			return h
		}
	}
	return ""
}

// fenceRun returns the length of the leading run of ` or ~ in t.
func fenceRun(t string) int {
	if t == "" || (t[0] != '`' && t[0] != '~') {
		return 0
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	return n
}
