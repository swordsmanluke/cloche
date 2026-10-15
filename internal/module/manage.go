package module

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// ModulesDir is the vendoring directory, relative to .cloche/.
const ModulesDir = "modules"

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Spec is a parsed module source argument.
type Spec struct {
	URL  string // git URL (empty for local paths)
	Ref  string // branch, tag or commit; empty means the remote HEAD
	Path string // local directory (empty for git sources)
}

// ParseSpec splits "<git-url>[@ref]" or a local path. Anything that exists as
// a directory, or starts with ".", "/" or "~", is a local path; everything
// else is treated as a git source.
func ParseSpec(arg string) (Spec, error) {
	if arg == "" {
		return Spec{}, errors.New("module source is required")
	}
	if strings.HasPrefix(arg, "path:") {
		return Spec{Path: strings.TrimPrefix(arg, "path:")}, nil
	}
	if !strings.Contains(arg, "://") && !strings.Contains(arg, "@") {
		return Spec{Path: arg}, nil
	}
	if strings.HasPrefix(arg, ".") || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "~") {
		if st, err := os.Stat(arg); err == nil && st.IsDir() {
			return Spec{Path: arg}, nil
		}
	}
	s := Spec{URL: arg}
	// A ref is a trailing "@x" where x has no '/' or ':' (so scp-style
	// "git@host:org/repo" isn't mistaken for a ref).
	if i := strings.LastIndex(arg, "@"); i > 0 {
		if tail := arg[i+1:]; tail != "" && !strings.ContainsAny(tail, "/:") {
			s.URL, s.Ref = arg[:i], tail
		}
	}
	if s.URL == "" || strings.HasPrefix(s.URL, "-") || strings.HasPrefix(s.Ref, "-") {
		return Spec{}, fmt.Errorf("invalid module source %q", arg)
	}
	return s, nil
}

// HashDir returns a content hash over every file under dir (sorted relative
// paths, modes and contents), ignoring .git.
func HashDir(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", err
		}
		st, err := os.Lstat(filepath.Join(dir, rel))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00", filepath.ToSlash(rel), st.Mode().Perm(), len(data))
		h.Write(data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// ReadLock returns the entries of .cloche/modules.lock under projectDir.
func ReadLock(projectDir string) ([]LockEntry, error) {
	return readLock(filepath.Join(projectDir, ".cloche", LockFile))
}

// WriteLock writes entries (sorted by name) to .cloche/modules.lock. An empty
// list removes the file.
func WriteLock(projectDir string, entries []LockEntry) error {
	path := filepath.Join(projectDir, ".cloche", LockFile)
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	sorted := append([]LockEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var buf bytes.Buffer
	buf.WriteString("# Managed by `cloche module`. Edit with care.\n")
	if err := toml.NewEncoder(&buf).Encode(lockfile{Modules: sorted}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func findEntry(entries []LockEntry, name string) int {
	for i, e := range entries {
		if e.Name == name {
			return i
		}
	}
	return -1
}

// Fetched is a module checked out into a temporary directory. Nothing in it
// has been executed.
type Fetched struct {
	Dir     string // module root, without .git
	Commit  string
	Name    string
	cleanup func()
}

// Close removes the temporary checkout.
func (f *Fetched) Close() { f.cleanup() }

func git(dir string, args ...string) (string, error) {
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "protocol.ext.allow=never",
		"-c", "core.fsmonitor=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Fetch clones url, resolves ref (default: remote HEAD) to a commit and
// materializes that commit's tree without .git. The module manifest is
// parsed and validated. The caller must Close the result.
func Fetch(url, ref string) (*Fetched, error) {
	tmp, err := os.MkdirTemp("", "cloche-module-")
	if err != nil {
		return nil, err
	}
	f := &Fetched{cleanup: func() { os.RemoveAll(tmp) }}
	fail := func(err error) (*Fetched, error) { f.Close(); return nil, err }

	repo := filepath.Join(tmp, "repo")
	if _, err := git(tmp, "clone", "--quiet", "--no-checkout", "--", url, repo); err != nil {
		return fail(err)
	}
	target := "HEAD"
	if ref != "" {
		target = ref
	}
	commit := ""
	for _, cand := range []string{target + "^{commit}", "origin/" + target + "^{commit}"} {
		if c, err := git(repo, "rev-parse", "--verify", "--quiet", cand); err == nil && c != "" {
			commit = c
			break
		}
	}
	if commit == "" {
		return fail(fmt.Errorf("cannot resolve ref %q in %s", target, url))
	}

	tree := filepath.Join(tmp, "tree")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return fail(err)
	}
	// git archive streams tracked files only; no filters, hooks or
	// submodules are run.
	archive := exec.Command("git", "-C", repo, "archive", "--format=tar", commit)
	untar := exec.Command("tar", "-x", "-C", tree)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	untar.Stdin = pipe
	var eb bytes.Buffer
	archive.Stderr, untar.Stderr = &eb, &eb
	if err := untar.Start(); err != nil {
		return fail(err)
	}
	if err := archive.Start(); err != nil {
		return fail(err)
	}
	aerr := archive.Wait()
	uerr := untar.Wait()
	if aerr != nil || uerr != nil {
		return fail(fmt.Errorf("export %s: %v %v: %s", commit, aerr, uerr, strings.TrimSpace(eb.String())))
	}

	if err := rejectSymlinks(tree); err != nil {
		return fail(err)
	}
	m, err := loadModule(tree, true)
	if err != nil {
		return fail(fmt.Errorf("not a valid module: %w", err))
	}
	if !nameRe.MatchString(m.Name) {
		return fail(fmt.Errorf("invalid module name %q", m.Name))
	}
	f.Dir, f.Commit, f.Name = tree, commit, m.Name
	return f, nil
}

func rejectSymlinks(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			rel, _ := filepath.Rel(dir, p)
			return fmt.Errorf("module contains a symlink (%s); refusing to vendor", rel)
		}
		return nil
	})
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported file %s", rel)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		st, err := in.Stat()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// replaceDir atomically-ish swaps dst for a copy of src.
func replaceDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	staging := dst + ".new"
	os.RemoveAll(staging)
	if err := copyTree(src, staging); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		os.RemoveAll(staging)
		return err
	}
	return os.Rename(staging, dst)
}

// AddResult describes an installed module.
type AddResult struct {
	Entry         LockEntry
	Prerequisites string // README "Prerequisites" section, if any
}

// Add installs the module described by spec into projectDir. Git sources are
// vendored under .cloche/modules/<name>/; local paths are recorded in place.
func Add(projectDir string, spec Spec) (*AddResult, error) {
	entries, err := ReadLock(projectDir)
	if err != nil {
		return nil, err
	}
	var entry LockEntry
	var readmeDir string

	if spec.Path != "" {
		p := spec.Path
		if strings.HasPrefix(p, "~/") {
			if home, herr := os.UserHomeDir(); herr == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		m, err := loadModule(abs, true)
		if err != nil {
			return nil, fmt.Errorf("not a valid module: %w", err)
		}
		if !nameRe.MatchString(m.Name) {
			return nil, fmt.Errorf("invalid module name %q", m.Name)
		}
		if findEntry(entries, m.Name) >= 0 {
			return nil, fmt.Errorf("module %q is already installed", m.Name)
		}
		if _, err := os.Stat(filepath.Join(projectDir, ".cloche", ModulesDir, m.Name)); err == nil {
			return nil, fmt.Errorf("module %q is already vendored", m.Name)
		}
		recorded := abs
		if absProj, perr := filepath.Abs(projectDir); perr == nil {
			if rel, rerr := filepath.Rel(absProj, abs); rerr == nil {
				recorded = rel
			}
		}
		entry = LockEntry{Name: m.Name, Source: pathPrefix + filepath.ToSlash(recorded)}
		readmeDir = abs
	} else {
		f, err := Fetch(spec.URL, spec.Ref)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if findEntry(entries, f.Name) >= 0 {
			return nil, fmt.Errorf("module %q is already installed", f.Name)
		}
		dst := filepath.Join(projectDir, ".cloche", ModulesDir, f.Name)
		if _, err := os.Stat(dst); err == nil {
			return nil, fmt.Errorf("%s already exists", dst)
		}
		if err := replaceDir(f.Dir, dst); err != nil {
			return nil, err
		}
		hash, err := HashDir(dst)
		if err != nil {
			return nil, err
		}
		entry = LockEntry{Name: f.Name, Source: spec.URL, Ref: spec.Ref, Commit: f.Commit, Hash: hash}
		readmeDir = dst
	}

	if err := WriteLock(projectDir, append(entries, entry)); err != nil {
		return nil, err
	}
	return &AddResult{Entry: entry, Prerequisites: Prerequisites(readmeDir)}, nil
}

// Prerequisites returns the body of the README's "Prerequisites" section
// (heading text matched case-insensitively), or "".
func Prerequisites(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		return ""
	}
	var out []string
	level := 0
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trim, "#") {
			n := len(trim) - len(strings.TrimLeft(trim, "#"))
			title := strings.ToLower(strings.TrimSpace(trim[n:]))
			if in && n <= level {
				break
			}
			if !in && strings.HasPrefix(title, "prerequisite") {
				in, level = true, n
				continue
			}
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// UpdatePlan is a fetched, not-yet-applied update.
type UpdatePlan struct {
	Entry     LockEntry // current lock entry
	New       LockEntry // entry that Apply will record
	Diff      string    // unified diff of vendored files, empty if identical
	LocalEdit bool      // vendored dir no longer matches the recorded hash
	fetched   *Fetched
	dst       string
}

// Close releases the temporary checkout.
func (p *UpdatePlan) Close() {
	if p.fetched != nil {
		p.fetched.Close()
	}
}

// PlanUpdate fetches the latest commit for a vendored module (re-resolving
// its recorded ref, or newRef if given) and computes the diff against the
// vendored copy. Nothing is modified.
func PlanUpdate(projectDir, name, newRef string) (*UpdatePlan, error) {
	entries, err := ReadLock(projectDir)
	if err != nil {
		return nil, err
	}
	i := findEntry(entries, name)
	if i < 0 {
		return nil, fmt.Errorf("module %q is not installed", name)
	}
	e := entries[i]
	if _, local := e.LocalPath(); local {
		return nil, fmt.Errorf("module %q is a local-path module used in place; nothing to update", name)
	}
	ref := e.Ref
	if newRef != "" {
		ref = newRef
	}
	f, err := Fetch(e.Source, ref)
	if err != nil {
		return nil, err
	}
	if f.Name != name {
		f.Close()
		return nil, fmt.Errorf("source now declares module name %q, expected %q", f.Name, name)
	}
	dst := filepath.Join(projectDir, ".cloche", ModulesDir, name)
	plan := &UpdatePlan{Entry: e, fetched: f, dst: dst}
	hash, err := HashDir(f.Dir)
	if err != nil {
		plan.Close()
		return nil, err
	}
	plan.New = LockEntry{Name: name, Source: e.Source, Ref: ref, Commit: f.Commit, Hash: hash}
	if cur, err := HashDir(dst); err == nil {
		plan.LocalEdit = e.Hash != "" && cur != e.Hash
	}
	diff, err := diffDirs(dst, f.Dir)
	if err != nil {
		plan.Close()
		return nil, err
	}
	plan.Diff = diff
	return plan, nil
}

// Apply vendors the planned update and rewrites the lockfile.
func (p *UpdatePlan) Apply(projectDir string) error {
	if err := replaceDir(p.fetched.Dir, p.dst); err != nil {
		return err
	}
	entries, err := ReadLock(projectDir)
	if err != nil {
		return err
	}
	if i := findEntry(entries, p.New.Name); i >= 0 {
		entries[i] = p.New
	} else {
		entries = append(entries, p.New)
	}
	return WriteLock(projectDir, entries)
}

// diffDirs returns `diff -ruN old new` output with a/ and b/ style labels.
func diffDirs(oldDir, newDir string) (string, error) {
	tmp, err := os.MkdirTemp("", "cloche-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if _, err := os.Stat(oldDir); err == nil {
		if err := copyTree(oldDir, filepath.Join(tmp, "a")); err != nil {
			return "", err
		}
	} else if err := os.Mkdir(filepath.Join(tmp, "a"), 0o755); err != nil {
		return "", err
	}
	if err := copyTree(newDir, filepath.Join(tmp, "b")); err != nil {
		return "", err
	}
	cmd := exec.Command("diff", "-ruN", "a", "b")
	cmd.Dir = tmp
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
		return "", fmt.Errorf("diff: %w", err)
	}
	return out.String(), nil
}

// Remove uninstalls a module: its lock entry, and its vendored directory for
// git modules. Local-path module directories are never touched.
func Remove(projectDir, name string) (LockEntry, error) {
	entries, err := ReadLock(projectDir)
	if err != nil {
		return LockEntry{}, err
	}
	i := findEntry(entries, name)
	if i < 0 {
		return LockEntry{}, fmt.Errorf("module %q is not installed", name)
	}
	e := entries[i]
	if _, local := e.LocalPath(); !local {
		if !nameRe.MatchString(name) {
			return LockEntry{}, fmt.Errorf("invalid module name %q", name)
		}
		if err := os.RemoveAll(filepath.Join(projectDir, ".cloche", ModulesDir, name)); err != nil {
			return LockEntry{}, err
		}
	}
	entries = append(entries[:i], entries[i+1:]...)
	return e, WriteLock(projectDir, entries)
}
