package docker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// mergeMarker sits immediately before a file's final extension to mark an
// override that is merged into its target instead of replacing it:
// .claude/settings.merge.json merges into .claude/settings.json.
const mergeMarker = ".merge"

// configCodec parses and serialises one config file format. decode must
// return a map for the document's top level, or an error.
type configCodec struct {
	decode func([]byte) (map[string]any, error)
	encode func(map[string]any) ([]byte, error)
}

var mergeCodecs = map[string]configCodec{
	".json": {decodeJSON, encodeJSON},
	".yaml": {decodeYAML, encodeYAML},
	".yml":  {decodeYAML, encodeYAML},
	".toml": {decodeTOML, encodeTOML},
}

// mergeTarget reports whether name is a merge override and, if so, the name
// of the file it merges into. A name with the marker but an unsupported
// extension, or nothing before the marker, is an error rather than a plain
// copy: the author asked for a merge that cloche cannot do.
func mergeTarget(name string) (target string, isMerge bool, err error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if !strings.HasSuffix(stem, mergeMarker) {
		return "", false, nil
	}
	base := strings.TrimSuffix(stem, mergeMarker)
	if base == "" {
		return "", true, fmt.Errorf("%s: merge file needs a name before %q", name, mergeMarker)
	}
	if _, ok := mergeCodecs[strings.ToLower(ext)]; !ok {
		return "", true, fmt.Errorf("%s: cannot merge %q files (supported: .json, .yaml, .yml, .toml)", name, ext)
	}
	return base + ext, true, nil
}

// stageOverrides builds in stageDir the tree that is copied onto /workspace:
// plain files from .cloche/overrides/ as they are, and each merge file merged
// into its target. The target is the project file the container receives;
// one that patterns keep out of the container, or that does not exist, is
// treated as an empty document so no host file the container was not meant
// to see is read. Every problem found is returned (joined), and any problem
// means the overrides must not be applied.
func stageOverrides(projectDir string, patterns []ignorePattern, stageDir string) error {
	overridesDir := filepath.Join(projectDir, ".cloche", "overrides")
	if _, err := os.Stat(overridesDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	type mergeJob struct{ src, target string } // slash paths relative to overridesDir
	var jobs []mergeJob
	plain := map[string]bool{}
	var errs []error

	walkErr := filepath.WalkDir(overridesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(overridesDir, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(stageDir, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, 0755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			plain[filepath.ToSlash(rel)] = true
			return os.Symlink(link, dst)
		case !d.Type().IsRegular():
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		target, isMerge, err := mergeTarget(d.Name())
		if err != nil {
			// err already names the file; prefix its directory.
			errs = append(errs, fmt.Errorf(".cloche/overrides/%s%w", strings.TrimSuffix(relSlash, d.Name()), err))
			return nil
		}
		if isMerge {
			jobs = append(jobs, mergeJob{src: relSlash, target: path.Join(path.Dir(relSlash), target)})
			return nil
		}
		plain[relSlash] = true
		return copyRegularFile(p, dst)
	})
	if walkErr != nil {
		return fmt.Errorf("staging .cloche/overrides: %w", walkErr)
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].src < jobs[j].src })
	for _, job := range jobs {
		if plain[job.target] {
			errs = append(errs, fmt.Errorf(".cloche/overrides/%s and .cloche/overrides/%s both target %s; keep one",
				job.src, job.target, job.target))
			continue
		}
		if err := stageMerge(projectDir, patterns, filepath.Join(overridesDir, filepath.FromSlash(job.src)),
			job.src, job.target, filepath.Join(stageDir, filepath.FromSlash(job.target))); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// stageMerge merges the override at srcPath into the project file at
// targetRel and writes the result to dst with the override's file mode.
func stageMerge(projectDir string, patterns []ignorePattern, srcPath, srcRel, targetRel, dst string) error {
	codec := mergeCodecs[strings.ToLower(path.Ext(targetRel))]

	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	patch, err := codec.decode(data)
	if err != nil {
		return fmt.Errorf(".cloche/overrides/%s: %w", srcRel, err)
	}

	doc := map[string]any{}
	if !excludedFromCopy(patterns, targetRel) {
		targetPath := filepath.Join(projectDir, filepath.FromSlash(targetRel))
		tinfo, err := os.Lstat(targetPath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		case !tinfo.Mode().IsRegular():
			return fmt.Errorf("%s: merge target is not a regular file (merged from .cloche/overrides/%s)", targetRel, srcRel)
		default:
			tdata, err := os.ReadFile(targetPath)
			if err != nil {
				return err
			}
			if doc, err = codec.decode(tdata); err != nil {
				return fmt.Errorf("%s: %w (merge target of .cloche/overrides/%s)", targetRel, err, srcRel)
			}
		}
	}

	merged, err := codec.encode(mergePatch(doc, patch).(map[string]any))
	if err != nil {
		return fmt.Errorf("writing merged %s: %w", targetRel, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, merged, info.Mode().Perm())
}

// mergePatch applies patch to target with JSON Merge Patch (RFC 7386)
// rules: maps merge recursively, a nil value deletes the key, and anything
// else (scalars and lists) replaces the target's value. target may be
// modified in place.
func mergePatch(target, patch any) any {
	pm, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	tm, ok := target.(map[string]any)
	if !ok {
		tm = map[string]any{}
	}
	for k, v := range pm {
		if v == nil {
			delete(tm, k)
			continue
		}
		tm[k] = mergePatch(tm[k], v)
	}
	return tm
}

func copyRegularFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func topLevelMap(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("top level must be a map/object, got %T", v)
	}
	return m, nil
}

func decodeJSON(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keep large integers exact
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid JSON: unexpected data after the top-level value")
	}
	return topLevelMap(v)
}

func encodeJSON(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeYAML(data []byte) (map[string]any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var v any
	if err := dec.Decode(&v); err == io.EOF {
		return map[string]any{}, nil // empty file
	} else if err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("YAML file holds more than one document; merge needs exactly one")
	}
	if err := checkStringKeys(v, ""); err != nil {
		return nil, err
	}
	return topLevelMap(v)
}

// checkStringKeys rejects YAML maps with non-string keys anywhere in v: they
// have no merge-patch meaning, and converting them would change the file.
func checkStringKeys(v any, at string) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if err := checkStringKeys(child, at+"."+k); err != nil {
				return err
			}
		}
	case map[any]any:
		if at == "" {
			at = "top level"
		}
		return fmt.Errorf("YAML map at %s has non-string keys; merge supports string keys only", strings.TrimPrefix(at, "."))
	case []any:
		for i, child := range t {
			if err := checkStringKeys(child, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func encodeYAML(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeTOML(data []byte) (map[string]any, error) {
	m := map[string]any{}
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	return m, nil
}

func encodeTOML(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ValidateOverrides runs the same staging as container start against the
// project's working tree (with .clocheignore applied, but no per-run repo
// selection) and returns every problem it finds, or nil.
func ValidateOverrides(projectDir string) []error {
	patterns, err := parseClocheignore(projectDir)
	if err != nil {
		return []error{fmt.Errorf("parsing .clocheignore: %w", err)}
	}
	stage, err := os.MkdirTemp("", "cloche-overrides-validate")
	if err != nil {
		return []error{err}
	}
	defer os.RemoveAll(stage)
	err = stageOverrides(projectDir, patterns, stage)
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
