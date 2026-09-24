package scan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/intent"
)

func TestParseDomainsCSV_HappyPathWithQuotedCommasAndNewlines(t *testing.T) {
	data := "name,description,paths\n" +
		"workflow-dsl,\"The .cloche workflow DSL — parser, validation, wiring.\",internal/dsl/**;docs/workflows.md\n" +
		"error-reporting,\"Shared `line N: <message>` formatting\nand exit codes.\",bract/errors.py\n"
	domains, problems := ParseDomainsCSV([]byte(data))
	require.Empty(t, problems)
	require.Len(t, domains, 2)
	assert.Equal(t, "workflow-dsl", domains[0].Name)
	assert.Equal(t, []string{"internal/dsl/**", "docs/workflows.md"}, domains[0].Paths)
	assert.Contains(t, domains[1].Description, "line N: <message>")
	assert.False(t, domains[0].UserEdited)
}

func TestParseDomainsCSV_ReportsEveryProblem(t *testing.T) {
	data := "name,description,paths\n" +
		"Bad Name,desc,a\n" +
		"ok,,\n" +
		"ok,desc,b\n" +
		"three,desc\n"
	domains, problems := ParseDomainsCSV([]byte(data))
	assert.Nil(t, domains)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, "line 2: name \"Bad Name\" must be kebab-case")
	assert.Contains(t, joined, "line 3: domain \"ok\" has an empty description")
	assert.Contains(t, joined, "line 3: domain \"ok\" has no paths")
	assert.Contains(t, joined, "line 4: duplicate domain name \"ok\"")
	assert.Contains(t, joined, "line 5: expected 3 columns")
}

func TestParseDomainsCSV_HeaderAndEmpty(t *testing.T) {
	_, problems := ParseDomainsCSV(nil)
	assert.Contains(t, problems[0], "empty")
	_, problems = ParseDomainsCSV([]byte("domain,desc,paths\nx,y,z\n"))
	assert.Contains(t, problems[0], "header must be exactly")
	_, problems = ParseDomainsCSV([]byte("name,description,paths\n"))
	assert.Contains(t, problems[0], "no domain rows")
	// A BOM and a trailing blank line are fine.
	domains, problems := ParseDomainsCSV(append([]byte{0xEF, 0xBB, 0xBF}, []byte("name,description,paths\nx,y,z\n\n")...))
	assert.Empty(t, problems)
	assert.Len(t, domains, 1)
}

func TestMergeDomains_UserEditedIsUntouchableByConstruction(t *testing.T) {
	existing := &intent.DomainMap{Version: 1, Domains: []intent.Domain{
		{Name: "versioning", Description: "human wrote this", Paths: []string{"internal/version/**"}, UserEdited: true},
		{Name: "stale", Description: "agent wrote this last time", Paths: []string{"old/**"}},
	}}
	proposed := []intent.Domain{
		{Name: "versioning", Description: "agent tries to rewrite", Paths: []string{"x"}},
		{Name: "workflow-dsl", Description: "new", Paths: []string{"internal/dsl/**"}},
	}
	merged, notes := MergeDomains(existing, proposed)
	require.Len(t, merged.Domains, 2)
	assert.Equal(t, "versioning", merged.Domains[0].Name)
	assert.Equal(t, "human wrote this", merged.Domains[0].Description)
	assert.True(t, merged.Domains[0].UserEdited)
	assert.Equal(t, "workflow-dsl", merged.Domains[1].Name)
	assert.Len(t, notes, 1)
	// "stale" was not in the proposal, so it is gone.
	for _, d := range merged.Domains {
		assert.NotEqual(t, "stale", d.Name)
	}
}

func TestMergeDomains_NoExisting(t *testing.T) {
	merged, notes := MergeDomains(nil, []intent.Domain{{Name: "a", Description: "d", Paths: []string{"p"}}})
	assert.Equal(t, 1, merged.Version)
	assert.Len(t, merged.Domains, 1)
	assert.Empty(t, notes)
}
