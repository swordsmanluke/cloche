package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/intent"
	"github.com/swordsmanluke/cloche/internal/promptrev"
)

// apiLedgerPoint is one day's pass-rate bucket in the ledger's
// pass-rate-over-time series.
type apiLedgerPoint struct {
	Date     string  `json:"date"`
	Attempts int     `json:"attempts"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`
}

// apiLedgerRevision is one prompt file revision's outcome stats.
type apiLedgerRevision struct {
	Revision   string  `json:"revision"`
	Date       string  `json:"date,omitempty"`
	Message    string  `json:"message,omitempty"`
	Attempts   int     `json:"attempts"`
	Passed     int     `json:"passed"`
	PassRate   float64 `json:"pass_rate"`
	MeanTokens float64 `json:"mean_tokens"`
}

// apiLedgerComparison compares the two most recent revisions of a prompt
// file, for the "did the latest edit help or hurt" question.
type apiLedgerComparison struct {
	Before apiLedgerRevision `json:"before"`
	After  apiLedgerRevision `json:"after"`
}

// apiLedgerPromptFile is one prompt file's revision history and outcomes.
type apiLedgerPromptFile struct {
	Path         string               `json:"path"`
	Revisions    []apiLedgerRevision  `json:"revisions"` // newest first
	LatestChange *apiLedgerComparison `json:"latest_change,omitempty"`
}

// apiLedgerRequirementTask is one task that cited a requirement.
type apiLedgerRequirementTask struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
}

// apiLedgerRequirement is one standing requirement and the tasks that ran
// with it injected into their prompt.
type apiLedgerRequirement struct {
	ID        string                     `json:"id"`
	Statement string                     `json:"statement,omitempty"`
	Tasks     []apiLedgerRequirementTask `json:"tasks"`
}

// apiLedgerTaskRequirements is one task and the requirement IDs it ran
// under (the reverse of apiLedgerRequirement.Tasks).
type apiLedgerTaskRequirements struct {
	TaskID       string   `json:"task_id"`
	Title        string   `json:"title,omitempty"`
	Status       string   `json:"status,omitempty"`
	Requirements []string `json:"requirement_ids"`
}

// apiLedgerResponse is the payload for GET /api/projects/{name}/ledger.
type apiLedgerResponse struct {
	PassRateOverTime       []apiLedgerPoint            `json:"pass_rate_over_time"`
	MeanAttemptsToSuccess  float64                     `json:"mean_attempts_to_success"`
	TokensPerSucceededTask float64                     `json:"tokens_per_succeeded_task"`
	PromptFiles            []apiLedgerPromptFile       `json:"prompt_files"`
	Requirements           []apiLedgerRequirement      `json:"requirements"`
	TaskRequirements       []apiLedgerTaskRequirements `json:"task_requirements"`
}

// ledgerAttempt is the flattened view of one attempt used by the
// aggregations below: which task it belongs to, its outcome/timing, and its
// total token usage across every run tied to it.
type ledgerAttempt struct {
	id, taskID string
	result     domain.AttemptResult
	startedAt  time.Time
	tokens     int64
}

// ledgerKV is the per-attempt slice of context_kv data the ledger cares
// about: prompt file/revision pairs (keyed by "<workflow>:<step>") and the
// set of requirement IDs injected into this attempt's steps.
type ledgerKV struct {
	promptFile map[string]string
	promptRev  map[string]string
	reqIDs     map[string]bool
}

// revStat accumulates outcome counts for one (prompt file, revision) pair.
type revStat struct {
	attempts, passed int
	tokenSum         int64
}

// isTerminal reports whether an attempt result should count toward ledger
// stats (a still-running attempt has no outcome yet).
func ledgerIsTerminal(r domain.AttemptResult) bool {
	switch r {
	case domain.AttemptResultSucceeded, domain.AttemptResultFailed, domain.AttemptResultCancelled:
		return true
	default:
		return false
	}
}

// handleAPILedger serves the per-project ledger view: pass rate over time,
// tokens per succeeded task, mean attempts to success, prompt-revision
// outcomes, and requirement-injection cross-references. See
// docs/plans for the design; the underlying data is recorded at dispatch
// time by host.Executor.recordPromptRevisionKV / seedIntentKV and their
// grpc.DaemonExecutor counterparts, with best-effort backfill (via git log
// timestamps) for attempts that predate that recording.
func (h *Handler) handleAPILedger(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	resp := apiLedgerResponse{
		PassRateOverTime: []apiLedgerPoint{},
		PromptFiles:      []apiLedgerPromptFile{},
		Requirements:     []apiLedgerRequirement{},
		TaskRequirements: []apiLedgerTaskRequirements{},
	}

	if h.taskStore == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	tasks, err := h.taskStore.ListTasks(ctx, dir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tokenTotals, _ := h.store.AttemptTokenTotals(ctx, dir)
	kvRows, _ := h.store.ListContextKVForProject(ctx, dir)

	perAttempt := map[string]*ledgerKV{}
	kvFor := func(attemptID string) *ledgerKV {
		k := perAttempt[attemptID]
		if k == nil {
			k = &ledgerKV{promptFile: map[string]string{}, promptRev: map[string]string{}, reqIDs: map[string]bool{}}
			perAttempt[attemptID] = k
		}
		return k
	}
	for _, row := range kvRows {
		switch {
		case strings.HasSuffix(row.Key, ":prompt_file"):
			kvFor(row.AttemptID).promptFile[strings.TrimSuffix(row.Key, ":prompt_file")] = row.Value
		case strings.HasSuffix(row.Key, ":prompt_rev"):
			kvFor(row.AttemptID).promptRev[strings.TrimSuffix(row.Key, ":prompt_rev")] = row.Value
		case strings.HasSuffix(row.Key, ":intent"):
			ids := kvFor(row.AttemptID).reqIDs
			for _, id := range strings.Split(row.Value, ",") {
				if id = strings.TrimSpace(id); id != "" {
					ids[id] = true
				}
			}
		}
	}

	taskByID := map[string]*domain.Task{}
	var attempts []ledgerAttempt
	for _, t := range tasks {
		taskByID[t.ID] = t
		for _, a := range t.Attempts {
			attempts = append(attempts, ledgerAttempt{
				id: a.ID, taskID: t.ID, result: a.Result,
				startedAt: a.StartedAt, tokens: tokenTotals[a.ID],
			})
		}
	}

	fillLedgerSummary(&resp, tasks, tokenTotals)

	fileRevStats := map[string]map[string]*revStat{}
	attemptFilesRecorded := map[string]bool{}
	for _, a := range attempts {
		kv := perAttempt[a.id]
		if kv == nil {
			continue
		}
		for prefix, file := range kv.promptFile {
			rev := kv.promptRev[prefix]
			if rev == "" {
				continue
			}
			addRevStat(fileRevStats, file, rev, a)
			attemptFilesRecorded[a.id] = true
		}
	}
	// Backfill: attempts with no live-recorded prompt revision at all (they
	// predate recordPromptRevisionKV) are attributed to the prompt file(s)
	// their executed steps reference today, at the git revision effective
	// at the attempt's start time.
	for _, a := range attempts {
		if attemptFilesRecorded[a.id] || !ledgerIsTerminal(a.result) {
			continue
		}
		backfillPromptRevisions(h, ctx, dir, a, fileRevStats)
	}
	resp.PromptFiles = buildLedgerPromptFiles(dir, fileRevStats)

	reqToTasks := map[string]map[string]bool{}
	taskToReqs := map[string]map[string]bool{}
	for _, a := range attempts {
		kv := perAttempt[a.id]
		if kv == nil {
			continue
		}
		for id := range kv.reqIDs {
			if reqToTasks[id] == nil {
				reqToTasks[id] = map[string]bool{}
			}
			reqToTasks[id][a.taskID] = true
			if taskToReqs[a.taskID] == nil {
				taskToReqs[a.taskID] = map[string]bool{}
			}
			taskToReqs[a.taskID][id] = true
		}
	}
	resp.Requirements, resp.TaskRequirements = buildLedgerRequirements(dir, taskByID, reqToTasks, taskToReqs)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// fillLedgerSummary computes the three headline stats: pass rate per day
// (across terminal attempts), mean attempts-to-first-success per task (over
// tasks that have succeeded at least once), and mean total token usage of
// succeeded attempts.
func fillLedgerSummary(resp *apiLedgerResponse, tasks []*domain.Task, tokenTotals map[string]int64) {
	dayStats := map[string]*apiLedgerPoint{}
	var attemptsToSuccessTotal, attemptsToSuccessCount int
	var succeededTokenTotal int64
	var succeededCount int

	for _, t := range tasks {
		for _, a := range t.Attempts {
			if !ledgerIsTerminal(a.Result) {
				continue
			}
			day := a.StartedAt.Format("2006-01-02")
			p := dayStats[day]
			if p == nil {
				p = &apiLedgerPoint{Date: day}
				dayStats[day] = p
			}
			p.Attempts++
			if a.Result == domain.AttemptResultSucceeded {
				p.Passed++
			}
		}
		// "Tokens per succeeded task" and "mean attempts to success" both key
		// off the first success per task, so a task that (unusually) has more
		// than one succeeded attempt is still counted once.
		for i, a := range t.Attempts {
			if a.Result == domain.AttemptResultSucceeded {
				attemptsToSuccessTotal += i + 1
				attemptsToSuccessCount++
				succeededTokenTotal += tokenTotals[a.ID]
				succeededCount++
				break
			}
		}
	}

	days := make([]string, 0, len(dayStats))
	for d := range dayStats {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		p := dayStats[d]
		if p.Attempts > 0 {
			p.PassRate = float64(p.Passed) / float64(p.Attempts)
		}
		resp.PassRateOverTime = append(resp.PassRateOverTime, *p)
	}

	if attemptsToSuccessCount > 0 {
		resp.MeanAttemptsToSuccess = float64(attemptsToSuccessTotal) / float64(attemptsToSuccessCount)
	}
	if succeededCount > 0 {
		resp.TokensPerSucceededTask = float64(succeededTokenTotal) / float64(succeededCount)
	}
}

// addRevStat records one attempt's outcome against a (file, revision) pair.
func addRevStat(m map[string]map[string]*revStat, file, rev string, a ledgerAttempt) {
	if !ledgerIsTerminal(a.result) {
		return
	}
	byRev := m[file]
	if byRev == nil {
		byRev = map[string]*revStat{}
		m[file] = byRev
	}
	s := byRev[rev]
	if s == nil {
		s = &revStat{}
		byRev[rev] = s
	}
	s.attempts++
	if a.result == domain.AttemptResultSucceeded {
		s.passed++
	}
	s.tokenSum += a.tokens
}

// backfillPromptRevisions attributes a legacy attempt (one with no
// live-recorded prompt_rev KV) to the prompt file(s) its executed steps
// reference in the workflow definition today, at the git revision that was
// effective as of the attempt's start time. Best-effort: any lookup failure
// just skips that attempt/file.
func backfillPromptRevisions(h *Handler, ctx context.Context, dir string, a ledgerAttempt, fileRevStats map[string]map[string]*revStat) {
	runs, err := h.store.ListRunsFiltered(ctx, domain.RunListFilter{ProjectDir: dir, AttemptID: a.id})
	if err != nil {
		return
	}
	seenFiles := map[string]bool{}
	for _, run := range runs {
		files := promptrev.ResolveWorkflowPromptFiles(dir, run.WorkflowName)
		if len(files) == 0 || h.captures == nil {
			continue
		}
		execs, err := h.captures.GetCaptures(ctx, run.ID)
		if err != nil {
			continue
		}
		for _, se := range execs {
			relPath, ok := files[se.StepName]
			if !ok || seenFiles[relPath] {
				continue
			}
			rev := promptrev.GitRevisionAt(dir, relPath, a.startedAt)
			if rev == "" {
				continue
			}
			seenFiles[relPath] = true
			addRevStat(fileRevStats, relPath, rev, a)
		}
	}
}

// buildLedgerPromptFiles turns the accumulated per-(file,revision) stats
// into the API shape, ordering each file's revisions newest-first using
// `git log --follow` history and computing the before/after comparison for
// the most recent change that has recorded attempts.
func buildLedgerPromptFiles(dir string, fileRevStats map[string]map[string]*revStat) []apiLedgerPromptFile {
	files := make([]string, 0, len(fileRevStats))
	for f := range fileRevStats {
		files = append(files, f)
	}
	sort.Strings(files)

	var out []apiLedgerPromptFile
	for _, file := range files {
		revStats := fileRevStats[file]
		history := promptFileGitHistory(dir, file)

		var revisions []apiLedgerRevision
		seen := map[string]bool{}
		for _, c := range history {
			s, ok := revStats[c.sha]
			if !ok {
				continue
			}
			revisions = append(revisions, toAPILedgerRevision(c.sha, c.date, c.message, s))
			seen[c.sha] = true
		}
		// Revisions with recorded stats but missing from git history (e.g. a
		// shallow clone, or --follow losing track across an unusual rename)
		// are still worth surfacing, appended in a stable (sorted) order.
		var orphans []string
		for sha := range revStats {
			if !seen[sha] {
				orphans = append(orphans, sha)
			}
		}
		sort.Strings(orphans)
		for _, sha := range orphans {
			revisions = append(revisions, toAPILedgerRevision(sha, "", "", revStats[sha]))
		}

		pf := apiLedgerPromptFile{Path: file, Revisions: revisions}
		if len(revisions) >= 2 {
			pf.LatestChange = &apiLedgerComparison{Before: revisions[1], After: revisions[0]}
		}
		out = append(out, pf)
	}
	return out
}

func toAPILedgerRevision(sha, date, message string, s *revStat) apiLedgerRevision {
	rev := apiLedgerRevision{
		Revision: shortSHA(sha),
		Date:     date,
		Message:  message,
		Attempts: s.attempts,
		Passed:   s.passed,
	}
	if s.attempts > 0 {
		rev.PassRate = float64(s.passed) / float64(s.attempts)
		rev.MeanTokens = float64(s.tokenSum) / float64(s.attempts)
	}
	return rev
}

// promptFileCommit is one entry of a prompt file's git history, newest first.
type promptFileCommit struct {
	sha, date, message string
}

// promptFileGitHistory returns relPath's commit history within dir, newest
// first, or nil if dir isn't a git repo / the file has no history.
func promptFileGitHistory(dir, relPath string) []promptFileCommit {
	cmd := exec.Command("git", "log", "--follow", "--format=%H%x1f%aI%x1f%s", "--", relPath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var history []promptFileCommit
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 3 {
			continue
		}
		date := parts[1]
		if t, err := time.Parse(time.RFC3339, date); err == nil {
			date = t.Format("2006-01-02")
		}
		history = append(history, promptFileCommit{sha: parts[0], date: date, message: parts[2]})
	}
	return history
}

// buildLedgerRequirements turns the accumulated requirement<->task index
// maps into the API shape, loading requirement statements from the intent
// store when a .cloche/intent/ directory exists.
func buildLedgerRequirements(dir string, taskByID map[string]*domain.Task, reqToTasks, taskToReqs map[string]map[string]bool) ([]apiLedgerRequirement, []apiLedgerTaskRequirements) {
	statements := map[string]string{}
	if reqs, err := intent.NewStore(dir).ListRequirements(); err == nil {
		for _, req := range reqs {
			statements[req.ID] = req.Body
		}
	}

	reqIDs := make([]string, 0, len(reqToTasks))
	for id := range reqToTasks {
		reqIDs = append(reqIDs, id)
	}
	sort.Strings(reqIDs)

	requirements := make([]apiLedgerRequirement, 0, len(reqIDs))
	for _, id := range reqIDs {
		taskIDs := make([]string, 0, len(reqToTasks[id]))
		for tid := range reqToTasks[id] {
			taskIDs = append(taskIDs, tid)
		}
		sort.Strings(taskIDs)

		reqTasks := make([]apiLedgerRequirementTask, 0, len(taskIDs))
		for _, tid := range taskIDs {
			reqTasks = append(reqTasks, apiLedgerRequirementTask{
				TaskID: tid,
				Title:  taskTitle(taskByID[tid]),
				Status: taskStatus(taskByID[tid]),
			})
		}
		requirements = append(requirements, apiLedgerRequirement{
			ID:        id,
			Statement: statements[id],
			Tasks:     reqTasks,
		})
	}

	taskIDs := make([]string, 0, len(taskToReqs))
	for tid := range taskToReqs {
		taskIDs = append(taskIDs, tid)
	}
	sort.Strings(taskIDs)

	taskRequirements := make([]apiLedgerTaskRequirements, 0, len(taskIDs))
	for _, tid := range taskIDs {
		ids := make([]string, 0, len(taskToReqs[tid]))
		for id := range taskToReqs[tid] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		taskRequirements = append(taskRequirements, apiLedgerTaskRequirements{
			TaskID:       tid,
			Title:        taskTitle(taskByID[tid]),
			Status:       taskStatus(taskByID[tid]),
			Requirements: ids,
		})
	}

	return requirements, taskRequirements
}

func taskTitle(t *domain.Task) string {
	if t == nil {
		return ""
	}
	return t.Title
}

func taskStatus(t *domain.Task) string {
	if t == nil {
		return ""
	}
	return string(t.Status)
}
