package scan

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/swordsmanluke/cloche/internal/intent"
)

// The discover-domains agent hands off a CSV, not YAML: the file it writes
// is data for a script, never the final artifact. domains.yaml itself is
// only ever written by Store.SaveDomains (yaml.Marshal), so an agent's
// formatting cannot corrupt it. See ParseDomainsCSV for the accepted shape.

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

var domainNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const domainsCSVHeader = "name,description,paths"

// ParseDomainsCSV decodes the discover-domains step's domains.csv:
//
//	name,description,paths
//	workflow-dsl,"The .cloche workflow DSL — parser, validation, wiring.",internal/dsl/**;docs/workflows.md
//
// One row per domain; paths are separated by ';' within the third column.
// Standard CSV quoting applies (a field containing a comma, quote or newline
// is double-quoted). Every problem found is returned as a human-readable
// line so a repair step can hand the whole list back to the agent at once;
// a non-empty problems slice means the domains must not be used.
func ParseDomainsCSV(data []byte) (domains []intent.Domain, problems []string) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, utf8BOM)))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true

	header, err := r.Read()
	if err == io.EOF {
		return nil, []string{"the file is empty; expected a header line: " + domainsCSVHeader}
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("line 1: %v", err)}
	}
	if len(header) < 3 ||
		strings.ToLower(strings.TrimSpace(header[0])) != "name" ||
		strings.ToLower(strings.TrimSpace(header[1])) != "description" ||
		strings.ToLower(strings.TrimSpace(header[2])) != "paths" {
		return nil, []string{fmt.Sprintf("line 1: header must be exactly %q (got %q)", domainsCSVHeader, strings.Join(header, ","))}
	}

	seen := map[string]int{}
	line := 1
	for {
		rec, err := r.Read()
		line++
		if err == io.EOF {
			break
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if len(rec) != 3 {
			problems = append(problems, fmt.Sprintf("line %d: expected 3 columns (name,description,paths), got %d — quote any field that contains a comma", line, len(rec)))
			continue
		}
		name := strings.TrimSpace(rec[0])
		desc := strings.TrimSpace(rec[1])
		if !domainNameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("line %d: name %q must be kebab-case (lowercase letters, digits, single hyphens)", line, name))
		}
		if prev, dup := seen[name]; dup {
			problems = append(problems, fmt.Sprintf("line %d: duplicate domain name %q (first seen on line %d)", line, name, prev))
		}
		seen[name] = line
		if desc == "" {
			problems = append(problems, fmt.Sprintf("line %d: domain %q has an empty description", line, name))
		}
		var paths []string
		for _, p := range strings.Split(rec[2], ";") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		if len(paths) == 0 {
			problems = append(problems, fmt.Sprintf("line %d: domain %q has no paths (separate several with ';')", line, name))
		}
		domains = append(domains, intent.Domain{Name: name, Description: desc, Paths: paths})
	}
	if len(domains) == 0 && len(problems) == 0 {
		problems = append(problems, "no domain rows after the header")
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return domains, nil
}

// MergeDomains builds the domain map to save from the agent's proposed full
// list and the existing map. The hard rule — a user_edited domain is never
// modified or removed — is enforced here, by construction, rather than by
// instruction: existing user-edited entries are copied through verbatim and
// a proposed entry with the same name is ignored. Every other existing entry
// is replaced by the proposal (the agent lists the full desired set), so
// domains the agent dropped disappear.
func MergeDomains(existing *intent.DomainMap, proposed []intent.Domain) (*intent.DomainMap, []string) {
	out := &intent.DomainMap{Version: 1}
	if existing != nil && existing.Version > 0 {
		out.Version = existing.Version
	}
	locked := map[string]bool{}
	if existing != nil {
		for _, d := range existing.Domains {
			if d.UserEdited {
				locked[d.Name] = true
				out.Domains = append(out.Domains, d)
			}
		}
	}
	var notes []string
	for _, d := range proposed {
		if locked[d.Name] {
			notes = append(notes, fmt.Sprintf("kept user-edited domain %q unchanged (the proposal for it was ignored)", d.Name))
			continue
		}
		out.Domains = append(out.Domains, d)
	}
	return out, notes
}
