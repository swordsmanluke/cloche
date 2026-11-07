package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

func TestMergeTarget(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		isMerge, err bool
	}{
		{name: "settings.json"},
		{name: "settings.merge.json", target: "settings.json", isMerge: true},
		{name: "app.merge.yaml", target: "app.yaml", isMerge: true},
		{name: "app.merge.yml", target: "app.yml", isMerge: true},
		{name: "pyproject.merge.toml", target: "pyproject.toml", isMerge: true},
		{name: "a.b.merge.JSON", target: "a.b.JSON", isMerge: true},
		{name: "notes.merge"},   // marker is the final extension: plain copy
		{name: "merge.json"},    // no marker
		{name: "x.merger.json"}, // not the marker
		{name: "rules.merge.ini", isMerge: true, err: true},
		{name: "README.merge.md", isMerge: true, err: true},
		{name: ".merge.json", isMerge: true, err: true},
	} {
		target, isMerge, err := mergeTarget(tc.name)
		if target != tc.target || isMerge != tc.isMerge || (err != nil) != tc.err {
			t.Errorf("mergeTarget(%q) = %q, %v, %v; want %q, %v, err=%v", tc.name, target, isMerge, err, tc.target, tc.isMerge, tc.err)
		}
	}
}

// TestMergePatchRFC7386 runs the examples from RFC 7386 Appendix A.
func TestMergePatchRFC7386(t *testing.T) {
	for _, tc := range [][3]string{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
	} {
		var target, patch, want any
		for i, dst := range []*any{&target, &patch, &want} {
			if err := json.Unmarshal([]byte(tc[i]), dst); err != nil {
				t.Fatal(err)
			}
		}
		if got := mergePatch(target, patch); !reflect.DeepEqual(got, want) {
			t.Errorf("merge(%s, %s) = %v, want %s", tc[0], tc[1], got, tc[2])
		}
	}
}

// stageProject writes files under a temp project dir (paths relative to the
// project root) and returns it.
func stageProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	return dir
}

func runStage(t *testing.T, projectDir string) (string, error) {
	t.Helper()
	patterns, err := parseClocheignore(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	return stage, stageOverrides(projectDir, patterns, stage)
}

func readStaged(t *testing.T, stage, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading staged %s: %v", rel, err)
	}
	return string(b)
}

func TestStageOverridesJSON(t *testing.T) {
	project := stageProject(t, map[string]string{
		".claude/settings.json": `{
  "permissions": {"allow": ["Bash(ls)"], "deny": ["Bash(rm)"]},
  "model": "opus",
  "big": 12345678901234567890
}`,
		".cloche/overrides/.claude/settings.merge.json": `{
  "enabledPlugins": {"ponytail@ponytail": true},
  "permissions": {"allow": ["Bash(go test <pkg>)"], "deny": null},
  "model": null
}`,
		".cloche/overrides/CLAUDE.md": "container rules\n",
	})
	stage, err := runStage(t, project)
	if err != nil {
		t.Fatal(err)
	}

	got := readStaged(t, stage, ".claude/settings.json")
	want := `{
  "big": 12345678901234567890,
  "enabledPlugins": {
    "ponytail@ponytail": true
  },
  "permissions": {
    "allow": [
      "Bash(go test <pkg>)"
    ]
  }
}
`
	if got != want {
		t.Errorf("merged settings.json =\n%s\nwant\n%s", got, want)
	}
	if readStaged(t, stage, "CLAUDE.md") != "container rules\n" {
		t.Error("plain override not copied")
	}
	if _, err := os.Stat(filepath.Join(stage, ".claude", "settings.merge.json")); !os.IsNotExist(err) {
		t.Errorf("merge file itself must not be staged, stat err = %v", err)
	}
}

func TestStageOverridesYAML(t *testing.T) {
	project := stageProject(t, map[string]string{
		"config/app.yaml":                         "server:\n  port: 8080\n  host: localhost\nfeatures: [a, b]\nlegacy: true\n",
		".cloche/overrides/config/app.merge.yaml": "server:\n  port: 9090\nfeatures: [c]\nlegacy: ~\n",
	})
	stage, err := runStage(t, project)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(readStaged(t, stage, "config/app.yaml")), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"server":   map[string]any{"port": 9090, "host": "localhost"},
		"features": []any{"c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged app.yaml = %#v, want %#v", got, want)
	}
}

func TestStageOverridesTOML(t *testing.T) {
	project := stageProject(t, map[string]string{
		"pyproject.toml":                         "[tool.ruff]\nline-length = 88\nselect = [\"E\"]\n\n[project]\nname = \"demo\"\n",
		".cloche/overrides/pyproject.merge.toml": "[tool.ruff]\nline-length = 120\n",
	})
	stage, err := runStage(t, project)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if _, err := toml.Decode(readStaged(t, stage, "pyproject.toml"), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"tool":    map[string]any{"ruff": map[string]any{"line-length": int64(120), "select": []any{"E"}}},
		"project": map[string]any{"name": "demo"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged pyproject.toml = %#v, want %#v", got, want)
	}
}

// A missing target, or one .clocheignore keeps out of the container, merges
// into an empty document (CLOCHE-A102): the host file is never read.
func TestStageOverridesAbsentTargets(t *testing.T) {
	project := stageProject(t, map[string]string{
		".clocheignore":                       "secret.json\n",
		"secret.json":                         `{"token": "do-not-leak"}`,
		".cloche/overrides/secret.merge.json": `{"mode": "container", "drop": null}`,
		".cloche/overrides/new.merge.json":    `{"a": {"b": 1, "c": null}}`,
	})
	stage, err := runStage(t, project)
	if err != nil {
		t.Fatal(err)
	}
	if got := readStaged(t, stage, "secret.json"); strings.Contains(got, "do-not-leak") || !strings.Contains(got, `"mode": "container"`) || strings.Contains(got, "drop") {
		t.Errorf("ignored target must merge into empty doc, got:\n%s", got)
	}
	if got := readStaged(t, stage, "new.json"); got != "{\n  \"a\": {\n    \"b\": 1\n  }\n}\n" {
		t.Errorf("missing target: got\n%s", got)
	}
}

func TestStageOverridesErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"bad merge file": {map[string]string{
			".cloche/overrides/a.merge.json": `{"x": `,
		}, ".cloche/overrides/a.merge.json: invalid JSON"},
		"bad target": {map[string]string{
			"a.json":                         `{"x": 1,}`,
			".cloche/overrides/a.merge.json": `{"y": 2}`,
		}, "a.json: invalid JSON"},
		"trailing data": {map[string]string{
			".cloche/overrides/a.merge.json": `{"x": 1} {"y": 2}`,
		}, "unexpected data"},
		"top-level array": {map[string]string{
			"a.json":                         `[1, 2]`,
			".cloche/overrides/a.merge.json": `{"y": 2}`,
		}, "top level must be a map"},
		"unsupported format": {map[string]string{
			".cloche/overrides/sub/rules.merge.ini": "[a]\nb=c\n",
		}, ".cloche/overrides/sub/rules.merge.ini: cannot merge"},
		"copy and merge conflict": {map[string]string{
			".cloche/overrides/a.json":       `{}`,
			".cloche/overrides/a.merge.json": `{}`,
		}, "both target a.json"},
		"yaml multi-doc": {map[string]string{
			".cloche/overrides/a.merge.yaml": "a: 1\n---\nb: 2\n",
		}, "more than one document"},
		"yaml non-string keys": {map[string]string{
			"a.yaml":                         "ports:\n  1: x\n",
			".cloche/overrides/a.merge.yaml": "b: 2\n",
		}, "non-string keys"},
		"bad toml": {map[string]string{
			".cloche/overrides/a.merge.toml": "x = = 1\n",
		}, "invalid TOML"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runStage(t, stageProject(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateOverridesReportsEveryProblem(t *testing.T) {
	project := stageProject(t, map[string]string{
		".cloche/overrides/a.merge.json":  `{`,
		".cloche/overrides/b.merge.ini":   "",
		".cloche/overrides/ok.merge.json": `{"fine": true}`,
	})
	errs := ValidateOverrides(project)
	if len(errs) != 2 {
		t.Fatalf("want 2 errors, got %d: %v", len(errs), errs)
	}
	if ValidateOverrides(stageProject(t, map[string]string{"README.md": "x"})) != nil {
		t.Error("project without overrides should validate clean")
	}
}
