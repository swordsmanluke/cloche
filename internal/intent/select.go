package intent

import (
	"context"
	"sort"
	"strings"

	"github.com/cloche-dev/cloche/internal/intent/embed"
)

// topK is the number of semantic matches considered per selection, per the
// design's spike-tuned constant.
const topK = 5

// defaultSimilarityFloor is used when the resolved Embedder doesn't expose a
// per-model floor via the optional floorProvider interface (e.g. a test
// stub, or an adapter that hasn't opted in).
const defaultSimilarityFloor = 0.35

// defaultTokenBudget is intent.token_budget's default, per the design.
const defaultTokenBudget = 2000

// Query is what Select matches candidate requirements against for one step.
type Query struct {
	// TaskDescription, StepPromptName, and WorkflowName are concatenated (in
	// that order) as the text embedded for semantic retrieval. The full
	// resolved prompt body is deliberately excluded — it's dominated by
	// boilerplate, per the design's spike.
	TaskDescription string
	StepPromptName  string
	WorkflowName    string

	// Repos are the workflow's declared repos/paths, matched against each
	// domain's Paths globs to compute the implicit domain context.
	Repos []string
	// Domains is the explicit step/workflow `domains = [...]` config key,
	// unioned into the domain context regardless of path overlap.
	Domains []string
}

// Options tunes Select. Zero value uses the design's defaults, so callers
// only need to set what intent.token_budget in config.toml overrides.
type Options struct {
	TokenBudget int
}

// Selected is one requirement chosen for injection, with the score used to
// order it.
type Selected struct {
	Requirement *Requirement
	// Score is the cosine similarity for a semantic match, or -1 for a
	// requirement that was only picked up by deterministic scope matching
	// (it either wasn't in the semantic top-k, or scored below the floor).
	Score float32
}

// Result is the outcome of Select: the requirements to inject, in final
// order, plus how many in-scope requirements were dropped to fit the token
// budget.
type Result struct {
	Selected []Selected
	Omitted  int
}

// Select applies the design's selection pipeline to reqs for one step's
// query: status filter, deterministic scope match, semantic top-k with a
// per-model floor, merge + ordering, then token-budget truncation.
//
// domains is the project's domain map, used for path-based domain context.
// index and embedder drive semantic retrieval; either may be nil, in which
// case selection degrades to deterministic scoping only — matching the
// design's "never blocks a run on embedding availability" guarantee (the
// keyword adapter is meant to always be available in production, but tests
// and callers that only care about deterministic behavior can skip wiring
// one up).
func Select(ctx context.Context, reqs []*Requirement, domains []Domain, index *Index, embedder embed.Embedder, query Query, opts Options) (Result, error) {
	dctx := domainContext(domains, query)

	active := make([]*Requirement, 0, len(reqs))
	byID := make(map[string]*Requirement, len(reqs))
	for _, r := range reqs {
		if r.Status != StatusActive {
			continue
		}
		active = append(active, r)
		byID[r.ID] = r
	}

	deterministic := make(map[string]bool)
	for _, r := range active {
		if scopeMatches(r.Scope, dctx) {
			deterministic[r.ID] = true
		}
	}

	scores := make(map[string]float32)
	if embedder != nil && index != nil {
		qText := queryText(query)
		if qText != "" {
			qVec, err := index.EmbedQuery(ctx, qText)
			if err != nil {
				return Result{}, err
			}
			floor := similarityFloor(embedder)
			for _, m := range index.TopK(qVec, topK) {
				if m.Score < floor {
					continue
				}
				if _, ok := byID[m.ID]; !ok {
					continue // stale index entry for a requirement no longer active/present
				}
				scores[m.ID] = m.Score
			}
		}
	}

	seen := make(map[string]bool, len(deterministic)+len(scores))
	for id := range deterministic {
		seen[id] = true
	}
	for id := range scores {
		seen[id] = true
	}

	selected := make([]Selected, 0, len(seen))
	for id := range seen {
		score, ok := scores[id]
		if !ok {
			score = -1
		}
		selected = append(selected, Selected{Requirement: byID[id], Score: score})
	}

	sortSelected(selected)

	budget := opts.TokenBudget
	if budget <= 0 {
		budget = defaultTokenBudget
	}
	return applyBudget(selected, budget), nil
}

// EmbedText returns the text embedded for a requirement: its domain names,
// body (statement + rationale), and retrieval hints — the composition
// validated by the design's spike. Callers building the Index's Item slice
// (e.g. before calling index.Sync) should use this so the corpus and
// Select's queries are scored against the same composition.
func EmbedText(req *Requirement) string {
	var b strings.Builder
	if len(req.Scope.Domains) > 0 {
		b.WriteString(strings.Join(req.Scope.Domains, ", "))
		b.WriteString(": ")
	}
	b.WriteString(req.Body)
	for _, h := range req.Hints {
		b.WriteString("\n")
		b.WriteString(h)
	}
	return b.String()
}

// domainContext returns the set of domain names in scope for query: any
// domain whose Paths overlap one of query.Repos, unioned with the explicit
// query.Domains key.
func domainContext(domains []Domain, query Query) map[string]bool {
	ctx := make(map[string]bool, len(query.Domains))
	for _, d := range query.Domains {
		ctx[d] = true
	}
	for _, dom := range domains {
		if ctx[dom.Name] {
			continue
		}
		for _, repo := range query.Repos {
			if pathsOverlap(dom.Paths, repo) {
				ctx[dom.Name] = true
				break
			}
		}
	}
	return ctx
}

// pathsOverlap reports whether repoPath overlaps any of the domain path
// globs: true when one is a prefix of the other once glob wildcards are
// stripped, so a domain path of "internal/dsl/**" overlaps a declared repo
// of "internal/dsl" (or a subpath of it), and a coarser repo declaration
// like "internal" overlaps every domain rooted under it.
func pathsOverlap(globPaths []string, repoPath string) bool {
	repoPath = strings.TrimSuffix(repoPath, "/")
	for _, g := range globPaths {
		base := globBase(g)
		if base == "" {
			continue
		}
		if strings.HasPrefix(repoPath, base) || strings.HasPrefix(base, repoPath) {
			return true
		}
	}
	return false
}

// globBase strips a trailing glob suffix (e.g. "/**", "/*.go") from a path
// glob, returning the concrete path prefix used for the overlap check.
func globBase(glob string) string {
	if idx := strings.IndexAny(glob, "*?["); idx != -1 {
		glob = glob[:idx]
	}
	return strings.TrimSuffix(glob, "/")
}

// scopeMatches reports whether a requirement's scope is in the deterministic
// domain context: project-level requirements always match; domain-level
// ones match when any of their domains is in ctx.
func scopeMatches(scope Scope, ctx map[string]bool) bool {
	if scope.Level == ScopeLevelProject {
		return true
	}
	for _, d := range scope.Domains {
		if ctx[d] {
			return true
		}
	}
	return false
}

// queryText composes the text embedded for retrieval, per the design: the
// task description, the step's resolved prompt template name, and the
// workflow name — never the full prompt body.
func queryText(q Query) string {
	parts := make([]string, 0, 3)
	if q.TaskDescription != "" {
		parts = append(parts, q.TaskDescription)
	}
	if q.StepPromptName != "" {
		parts = append(parts, q.StepPromptName)
	}
	if q.WorkflowName != "" {
		parts = append(parts, q.WorkflowName)
	}
	return strings.Join(parts, "\n")
}

// floorProvider is an optional capability an Embedder adapter can implement
// to expose its own per-model similarity floor (the design's spike-tuned
// values — "the floor is a per-model constant owned by the adapter, not a
// user-facing knob"). Adapters that don't implement it get
// defaultSimilarityFloor.
type floorProvider interface {
	SimilarityFloor() float32
}

func similarityFloor(e embed.Embedder) float32 {
	if fp, ok := e.(floorProvider); ok {
		return fp.SimilarityFloor()
	}
	return defaultSimilarityFloor
}

// confidenceRank orders Confidence high-to-low for sorting.
var confidenceRank = map[Confidence]int{
	ConfidenceHigh:   3,
	ConfidenceMedium: 2,
	ConfidenceLow:    1,
}

// lessByConfidenceRecency orders deterministic-only matches (no qualifying
// semantic score) by confidence descending, then recency (Updated)
// descending, then ID for a stable tiebreak.
func lessByConfidenceRecency(a, b *Requirement) bool {
	ra, rb := confidenceRank[a.Confidence], confidenceRank[b.Confidence]
	if ra != rb {
		return ra > rb
	}
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.After(b.Updated)
	}
	return a.ID < b.ID
}

// sortSelected orders selected per the design: project-level requirements
// first, then the union of deterministic and semantic matches by similarity
// score, with deterministic-only matches (Score == -1) ranked by confidence
// then recency and placed after every scored match.
func sortSelected(selected []Selected) {
	sort.SliceStable(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		aProject := a.Requirement.Scope.Level == ScopeLevelProject
		bProject := b.Requirement.Scope.Level == ScopeLevelProject
		if aProject != bProject {
			return aProject
		}
		if aProject {
			return lessByConfidenceRecency(a.Requirement, b.Requirement)
		}

		aScored, bScored := a.Score >= 0, b.Score >= 0
		if aScored != bScored {
			return aScored
		}
		if aScored {
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			return a.Requirement.ID < b.Requirement.ID
		}
		return lessByConfidenceRecency(a.Requirement, b.Requirement)
	})
}

// approxTokens estimates a rendered line's token cost using the common
// ~4-characters-per-token heuristic. There's no tokenizer in this package
// (and the exact count depends on the consuming model), but the budget's
// purpose is capping prompt bloat, not exact accounting.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	if n := len(s) / 4; n > 0 {
		return n
	}
	return 1
}

// applyBudget keeps selected in order until the running approxTokens total
// would exceed budget, dropping the remainder and reporting how many were
// omitted.
func applyBudget(selected []Selected, budget int) Result {
	used := 0
	kept := make([]Selected, 0, len(selected))
	for i, s := range selected {
		cost := approxTokens(formatRequirementLine(s.Requirement))
		if used+cost > budget {
			return Result{Selected: kept, Omitted: len(selected) - i}
		}
		used += cost
		kept = append(kept, s)
	}
	return Result{Selected: kept, Omitted: 0}
}
