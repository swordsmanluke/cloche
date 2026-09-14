// Intent-continuity retrieval spike: does small-local-model embedding retrieval
// beat keyword overlap for selecting requirements relevant to a task prompt?
//
// Compares embedding models (via local ollama /api/embed) against a keyword
// Jaccard baseline on a labeled corpus. Reports hit@1, recall@3, recall@5, MRR,
// and a threshold sweep (precision/recall at cosine cutoffs) per model.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
)

type Requirement struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Text   string `json:"text"`
}

type Query struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Relevant []string `json:"relevant"`
}

type Corpus struct {
	Requirements []Requirement `json:"requirements"`
	Queries      []Query       `json:"queries"`
}

func embed(model string, texts []string) ([][]float64, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "input": texts})
	resp, err := http.Post("http://localhost:11434/api/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("model %s: got %d embeddings for %d texts", model, len(out.Embeddings), len(texts))
	}
	for _, v := range out.Embeddings {
		normalize(v)
	}
	return out.Embeddings, nil
}

func normalize(v []float64) {
	var s float64
	for _, x := range v {
		s += x * x
	}
	n := math.Sqrt(s)
	if n == 0 {
		return
	}
	for i := range v {
		v[i] /= n
	}
}

func cosine(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an the of to in on for from with and or is are was be been it this that as at by not no never must should can into over after before new my i we you") {
		stopwords[w] = true
	}
}

func tokens(s string) map[string]bool {
	out := map[string]bool{}
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			if w := b.String(); len(w) > 1 && !stopwords[w] {
				out[w] = true
			}
			b.Reset()
		}
	}
	if w := b.String(); len(w) > 1 && !stopwords[w] {
		out[w] = true
	}
	return out
}

// keyword baseline: overlap coefficient between query tokens and requirement tokens.
func keywordScore(q, r map[string]bool) float64 {
	inter := 0
	for w := range q {
		if r[w] {
			inter++
		}
	}
	min := len(q)
	if len(r) < min {
		min = len(r)
	}
	if min == 0 {
		return 0
	}
	return float64(inter) / float64(min)
}

type ranking struct {
	query  Query
	scores []scored // sorted desc
}

type scored struct {
	id    string
	score float64
}

func evaluate(name string, rankings []ranking, w *strings.Builder) {
	var hit1, r3, r5, mrr float64
	for _, rk := range rankings {
		rel := map[string]bool{}
		for _, id := range rk.query.Relevant {
			rel[id] = true
		}
		if rel[rk.scores[0].id] {
			hit1++
		}
		firstRank := 0
		found3, found5 := 0, 0
		for i, s := range rk.scores {
			if rel[s.id] {
				if firstRank == 0 {
					firstRank = i + 1
				}
				if i < 3 {
					found3++
				}
				if i < 5 {
					found5++
				}
			}
		}
		n := len(rk.query.Relevant)
		c3, c5 := n, n
		if c3 > 3 {
			c3 = 3
		}
		if c5 > 5 {
			c5 = 5
		}
		r3 += float64(found3) / float64(c3)
		r5 += float64(found5) / float64(c5)
		if firstRank > 0 {
			mrr += 1.0 / float64(firstRank)
		}
	}
	n := float64(len(rankings))
	fmt.Fprintf(w, "%-22s hit@1 %.2f   recall@3 %.2f   recall@5 %.2f   MRR %.2f\n",
		name, hit1/n, r3/n, r5/n, mrr/n)
}

func thresholdSweep(name string, rankings []ranking, w *strings.Builder) {
	fmt.Fprintf(w, "\n%s threshold sweep (cosine cutoff -> precision / recall over all query-req pairs):\n", name)
	for _, t := range []float64{0.30, 0.35, 0.40, 0.45, 0.50, 0.55, 0.60, 0.65} {
		tp, fp, fn := 0, 0, 0
		for _, rk := range rankings {
			rel := map[string]bool{}
			for _, id := range rk.query.Relevant {
				rel[id] = true
			}
			for _, s := range rk.scores {
				if s.score >= t {
					if rel[s.id] {
						tp++
					} else {
						fp++
					}
				} else if rel[s.id] {
					fn++
				}
			}
		}
		prec, rec := 0.0, 0.0
		if tp+fp > 0 {
			prec = float64(tp) / float64(tp+fp)
		}
		if tp+fn > 0 {
			rec = float64(tp) / float64(tp+fn)
		}
		fmt.Fprintf(w, "  >= %.2f   precision %.2f   recall %.2f   (selected %d)\n", t, prec, rec, tp+fp)
	}
}

func failures(name string, rankings []ranking, w *strings.Builder) {
	fmt.Fprintf(w, "\n%s misses (first relevant not in top 3):\n", name)
	any := false
	for _, rk := range rankings {
		rel := map[string]bool{}
		for _, id := range rk.query.Relevant {
			rel[id] = true
		}
		miss := true
		for i := 0; i < 3 && i < len(rk.scores); i++ {
			if rel[rk.scores[i].id] {
				miss = false
			}
		}
		if miss {
			any = true
			top := []string{}
			for i := 0; i < 3; i++ {
				top = append(top, fmt.Sprintf("%s(%.2f)", rk.scores[i].id, rk.scores[i].score))
			}
			fmt.Fprintf(w, "  %s %q  wanted %v  got %s\n", rk.query.ID, rk.query.Text, rk.query.Relevant, strings.Join(top, " "))
		}
	}
	if !any {
		fmt.Fprintf(w, "  none\n")
	}
}


// generate calls the model through /api/chat so its chat template is applied
// (bonsai models return empty text through raw /api/generate).
func generate(model, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
		"options":  map[string]any{"num_predict": 6000},
	})
	resp, err := http.Post("http://localhost:11434/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Message.Content), nil
}

// countBad reports empty or refusal-shaped generations.
func countBad(m map[string]string) int {
	n := 0
	for _, v := range m {
		lv := strings.ToLower(v)
		if v == "" || strings.HasPrefix(lv, "i can't") || strings.HasPrefix(lv, "i cannot") {
			n++
		}
	}
	return n
}

// cleanGeneration strips reasoning preamble some models leak (e.g. "Here's a
// thinking process: ...") by keeping only the trailing run of short phrase
// lines. Falls back to the raw text if that leaves too little.
func cleanGeneration(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			if len(kept) > 0 {
				break
			}
			continue
		}
		if len(l) > 120 || strings.Contains(l, "**") || strings.HasSuffix(l, ":") {
			break
		}
		kept = append([]string{l}, kept...)
	}
	if len(kept) >= 2 {
		return strings.Join(kept, "\n")
	}
	return s
}

// expandQueries rewrites each task prompt into the project systems/policies it
// touches, via a small local LLM. Simulates injection-time query expansion: the
// expander sees the domain map (available in production) but not the requirements.
// Results are cached in expansions.json so reruns don't re-generate.
func expandQueries(c Corpus, model string) (map[string]string, error) {
	cache := map[string]string{}
	cacheFile := "expansions-" + strings.ReplaceAll(model, ":", "_") + ".json"
	if raw, err := os.ReadFile(cacheFile); err == nil {
		json.Unmarshal(raw, &cache)
	}
	var domains []string
	seen := map[string]bool{}
	for _, r := range c.Requirements {
		if !seen[r.Domain] {
			seen[r.Domain] = true
			domains = append(domains, r.Domain)
		}
	}
	changed := false
	for _, q := range c.Queries {
		if _, ok := cache[q.ID]; ok {
			continue
		}
		prompt := fmt.Sprintf(`A coding task will be performed in a software project. The project's systems are: %s.

Task: %q

Which project systems, configuration areas, policies, or conventions might this task touch or risk violating? Answer with 3 to 6 short phrases, one per line. No explanations, no numbering.`,
			strings.Join(domains, ", "), q.Text)
		resp, err := generate(model, prompt)
		if err != nil {
			return nil, err
		}
		cache[q.ID] = resp
		changed = true
		raw, _ := json.MarshalIndent(cache, "", "  ")
		os.WriteFile(cacheFile, raw, 0644)
	}
	if changed {
		raw, _ := json.MarshalIndent(cache, "", "  ")
		os.WriteFile(cacheFile, raw, 0644)
	}
	for k, v := range cache {
		cache[k] = cleanGeneration(v)
	}
	return cache, nil
}

// generateHints writes per-requirement "applies when" phrases via a small local
// LLM — simulating the extract agent authoring retrieval hints at scan time.
// Cached in hints.json.
func generateHints(c Corpus, model string) (map[string]string, error) {
	cache := map[string]string{}
	cacheFile := "hints-" + strings.ReplaceAll(model, ":", "_") + ".json"
	if raw, err := os.ReadFile(cacheFile); err == nil {
		json.Unmarshal(raw, &cache)
	}
	changed := false
	for _, r := range c.Requirements {
		if _, ok := cache[r.ID]; ok {
			continue
		}
		prompt := fmt.Sprintf(`A software project has this rule:

%q

List 3 to 5 short phrases describing concrete developer tasks, questions, or problem symptoms where this rule applies. Use the words a developer would actually use when asking, not the rule's own wording. One phrase per line, no numbering, no explanations.`, r.Text)
		resp, err := generate(model, prompt)
		if err != nil {
			return nil, err
		}
		cache[r.ID] = resp
		changed = true
		raw, _ := json.MarshalIndent(cache, "", "  ")
		os.WriteFile(cacheFile, raw, 0644)
	}
	if changed {
		raw, _ := json.MarshalIndent(cache, "", "  ")
		os.WriteFile(cacheFile, raw, 0644)
	}
	for k, v := range cache {
		cache[k] = cleanGeneration(v)
	}
	return cache, nil
}

func main() {
	raw, err := os.ReadFile("corpus.json")
	if err != nil {
		panic(err)
	}
	var c Corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		panic(err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "corpus: %d requirements, %d queries\n\n", len(c.Requirements), len(c.Queries))

	// Keyword baseline.
	reqToks := make([]map[string]bool, len(c.Requirements))
	for i, r := range c.Requirements {
		reqToks[i] = tokens(r.Domain + " " + r.Text)
	}
	var kwRank []ranking
	for _, q := range c.Queries {
		qt := tokens(q.Text)
		var ss []scored
		for i, r := range c.Requirements {
			ss = append(ss, scored{r.ID, keywordScore(qt, reqToks[i])})
		}
		sort.Slice(ss, func(a, b int) bool { return ss[a].score > ss[b].score })
		kwRank = append(kwRank, ranking{q, ss})
	}
	evaluate("keyword-baseline", kwRank, &out)

	// Embedding models. Requirements embedded as "domain: text" per the design.
	// Prefixes follow each model's documented retrieval format.
	models := []struct {
		name, docPrefix, queryPrefix string
	}{
		{"all-minilm", "", ""},
		{"nomic-embed-text", "search_document: ", "search_query: "},
		{"embeddinggemma", "title: none | text: ", "task: search result | query: "},
	}
	genModel := os.Getenv("GEN_MODEL")
	if genModel == "" {
		genModel = "llama3.2:3b"
	}
	fmt.Fprintf(&out, "generator model: %s\n\n", genModel)
	expansions, err := expandQueries(c, genModel)
	if err != nil {
		fmt.Fprintf(&out, "query expansion unavailable: %v\n", err)
		expansions = map[string]string{}
	}
	hints, err := generateHints(c, genModel)
	if err != nil {
		fmt.Fprintf(&out, "hint generation unavailable: %v\n", err)
		hints = map[string]string{}
	}
	fmt.Fprintf(&out, "bad generations: %d/%d expansions, %d/%d hints\n\n",
		countBad(expansions), len(expansions), countBad(hints), len(hints))

	type variant struct {
		label      string
		withHints  bool
		withExpand bool
	}
	variants := []variant{
		{"", false, false},
		{"+expand", false, true},
		{"+hints", true, false},
		{"+hints+expand", true, true},
	}

	for _, m := range models {
		model := m.name
		var lastRank []ranking
		for _, v := range variants {
			if (v.withExpand && len(expansions) == 0) || (v.withHints && len(hints) == 0) {
				continue
			}
			reqTexts := make([]string, len(c.Requirements))
			for i, r := range c.Requirements {
				reqTexts[i] = m.docPrefix + r.Domain + ": " + r.Text
				if v.withHints {
					reqTexts[i] += "\nApplies when: " + hints[r.ID]
				}
			}
			reqVecs, err := embed(model, reqTexts)
			if err != nil {
				fmt.Fprintf(&out, "%s: ERROR %v\n", model, err)
				break
			}
			qTexts := make([]string, len(c.Queries))
			for i, q := range c.Queries {
				qTexts[i] = m.queryPrefix + q.Text
				if v.withExpand {
					qTexts[i] += "\n" + expansions[q.ID]
				}
			}
			qVecs, err := embed(model, qTexts)
			if err != nil {
				fmt.Fprintf(&out, "%s: ERROR %v\n", model, err)
				break
			}
			var rank []ranking
			for i, q := range c.Queries {
				var ss []scored
				for j, r := range c.Requirements {
					ss = append(ss, scored{r.ID, cosine(qVecs[i], reqVecs[j])})
				}
				sort.Slice(ss, func(a, b int) bool { return ss[a].score > ss[b].score })
				rank = append(rank, ranking{q, ss})
			}
			evaluate(model+v.label, rank, &out)
			if v.label == "+hints" {
				lastRank = rank
			}
		}
		if lastRank != nil {
			thresholdSweep(model+"+hints", lastRank, &out)
			failures(model+"+hints", lastRank, &out)
			fmt.Fprintln(&out)
		}
	}
	failures("keyword-baseline", kwRank, &out)

	fmt.Print(out.String())
	os.WriteFile("results.txt", []byte(out.String()), 0644)
}
