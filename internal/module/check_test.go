package module_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dsl"
	"github.com/swordsmanluke/cloche/internal/module"
)

func parseProject(t *testing.T, dir string, files map[string]string) map[string]*domain.Workflow {
	t.Helper()
	out := map[string]*domain.Workflow{}
	for name, src := range files {
		p := filepath.Join(dir, ".cloche", name)
		write(t, p, src)
		if name == module.ProjectFile {
			continue
		}
		wfs, err := dsl.ParseAll(src, dsl.WithSourceFile(p))
		require.NoError(t, err)
		for n, wf := range wfs {
			out[n] = wf
		}
	}
	return out
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

const reviewMod = `workflow code-review {
  contract {
    kv: child_branch
    env: REVIEW_MODEL
    out: review_path
  }
  step r {
    run = "true"
    results = [success, fail]
  }
  r:success -> done
  r:fail -> abort
}
`

func installModule(t *testing.T, dir, name, toml, src string, readme bool) {
	mod := filepath.Join(dir, ".cloche", "modules", name)
	write(t, filepath.Join(mod, "module.toml"), toml)
	write(t, filepath.Join(mod, "m.cloche"), src)
	if readme {
		write(t, filepath.Join(mod, "README.md"), "# "+name)
	}
}

func callerSrc(with string, prelude string) string {
	return `workflow main {
  step prep {
    run = "true"
    results = [success, fail]
  }
  step review {
    workflow_name = "code-review"
    ` + with + `
    results = [success, fail]
  }
  ` + prelude + `
  prep:success -> review
  prep:fail -> abort
  review:success -> done
  review:fail -> abort
}
`
}

func TestCheck_ModuleManifest(t *testing.T) {
	dir := t.TempDir()
	installModule(t, dir, "rev", `name = "rev"
exports = ["code-review", "ghost"]
requires = ["definitely-not-a-binary-xyz"]
credentials = ["CLOCHE_TEST_CRED_UNSET"]
`, `workflow code-review {
  step r {
    run = "true"
    results = [success, fail]
  }
  r:success -> done
  r:fail -> abort
}
`, false)
	rep := module.Check(dir, nil, nil)
	assert.True(t, anyContains(rep.Errors, `exports "ghost"`), rep.Errors)
	assert.True(t, anyContains(rep.Errors, "missing README"), rep.Errors)
	assert.True(t, anyContains(rep.Errors, "definitely-not-a-binary-xyz"), rep.Errors)
	assert.True(t, anyContains(rep.Warnings, "no contract"), rep.Warnings)
	assert.True(t, anyContains(rep.Warnings, "CLOCHE_TEST_CRED_UNSET"), rep.Warnings)
}

func TestCheck_LockHashDrift(t *testing.T) {
	dir := t.TempDir()
	installModule(t, dir, "rev", "name = \"rev\"\nexports = [\"code-review\"]\n", reviewMod, true)
	hash, err := module.ContentHash(filepath.Join(dir, ".cloche", "modules", "rev"))
	require.NoError(t, err)
	write(t, filepath.Join(dir, ".cloche", "modules.lock"), "[[module]]\nname = \"rev\"\nsource = \"git@example.com:x/rev.git\"\nhash = \""+hash+"\"\n")
	assert.False(t, anyContains(module.Check(dir, nil, nil).Warnings, "modules.lock"))

	write(t, filepath.Join(dir, ".cloche", "modules", "rev", "README.md"), "edited")
	assert.True(t, anyContains(module.Check(dir, nil, nil).Warnings, "modules.lock"))
}

func TestCheck_BindTargets(t *testing.T) {
	dir := t.TempDir()
	installModule(t, dir, "rev", "name = \"rev\"\nexports = [\"code-review\"]\n", reviewMod, true)
	proj := parseProject(t, dir, map[string]string{
		"main.cloche":    callerSrc("", "") + "workflow rev {\n  step a {\n    run = \"true\"\n    results = [success, fail]\n  }\n  a:success -> done\n  a:fail -> abort\n}\n",
		"project.cloche": "bind code-review = rev\nbind lint = nowhere\n",
	})
	rep := module.Check(dir, proj, nil)
	assert.True(t, anyContains(rep.Errors, "ambiguous"), rep.Errors)
	assert.True(t, anyContains(rep.Errors, "nowhere"), rep.Errors)
}

func TestCheck_Contracts(t *testing.T) {
	setup := func(t *testing.T, with, prelude string) *module.Report {
		dir := t.TempDir()
		installModule(t, dir, "rev", "name = \"rev\"\nexports = [\"code-review\"]\n", reviewMod, true)
		proj := parseProject(t, dir, map[string]string{
			"main.cloche":    callerSrc(with, prelude),
			"project.cloche": "bind code-review = rev\n",
		})
		return module.Check(dir, proj, nil)
	}

	rep := setup(t, "", "")
	assert.True(t, anyContains(rep.Warnings, "requires kv:child_branch"), rep.Warnings)
	assert.True(t, anyContains(rep.Warnings, "requires env:REVIEW_MODEL"), rep.Warnings)

	rep = setup(t, "with {\n      child_branch from branch\n      env.REVIEW_MODEL = \"opus\"\n    }", "")
	assert.Empty(t, rep.Errors)
	assert.Empty(t, rep.Warnings)

	rep = setup(t, "with {\n      child_branch from branch\n      env.REVIEW_MODEL = \"opus\"\n      x from out.nope\n    }", "")
	assert.True(t, anyContains(rep.Errors, "out.nope"), rep.Errors)
}

func TestCheck_ScriptProvidedInputWarns(t *testing.T) {
	dir := t.TempDir()
	installModule(t, dir, "rev", "name = \"rev\"\nexports = [\"code-review\"]\n", reviewMod, true)
	proj := parseProject(t, dir, map[string]string{
		"main.cloche":    callerSrc("with {\n      env.REVIEW_MODEL = \"opus\"\n    }", ""),
		"project.cloche": "bind code-review = rev\n",
	})
	rep := module.Check(dir, proj, nil)
	assert.Empty(t, rep.Errors)
	assert.True(t, anyContains(rep.Warnings, "upstream script step"), rep.Warnings)
}

func TestCheck_ResultsMustBeWired(t *testing.T) {
	dir := t.TempDir()
	installModule(t, dir, "rev", "name = \"rev\"\nexports = [\"code-review\"]\n", reviewMod, true)
	src := strings.Replace(callerSrc("", ""), "  review:fail -> abort\n", "", 1)
	proj := parseProject(t, dir, map[string]string{
		"main.cloche":    src,
		"project.cloche": "bind code-review = rev\n",
	})
	assert.True(t, anyContains(module.Check(dir, proj, nil).Errors, `result "fail"`))
}

func TestCheck_NoModules(t *testing.T) {
	rep := module.Check(t.TempDir(), nil, nil)
	assert.Empty(t, rep.Errors)
	assert.Empty(t, rep.Warnings)
}

func TestCheck_UnprovidedInputIsError(t *testing.T) {
	dir := t.TempDir()
	installModule(t, dir, "rev", "name = \"rev\"\nexports = [\"code-review\"]\n", reviewMod, true)
	proj := parseProject(t, dir, map[string]string{
		"main.cloche": `workflow main {
  step review {
    workflow_name = "code-review"
    results = [success, fail]
  }
  review:success -> done
  review:fail -> abort
}
`,
		"project.cloche": "bind code-review = rev\n",
	})
	rep := module.Check(dir, proj, nil)
	assert.True(t, anyContains(rep.Errors, "requires kv:child_branch, which nothing provides"), rep.Errors)
}
