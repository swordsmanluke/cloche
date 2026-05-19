package features_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cucumber/godog"
)

// extractState holds per-scenario state for extract_base_sha_reresolution scenarios.
type extractState struct {
	repoDir   string
	commits   map[string]string // label -> SHA
	worktree  *docker.ExtractWorktree
	results   []docker.ExtractResult
	errors    []error
	priorHead string
	gitEnv    []string
	cleanups  []func()
}

func newExtractState() *extractState {
	return &extractState{
		commits: make(map[string]string),
		gitEnv: append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		),
	}
}

func (s *extractState) runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.repoDir
	cmd.Env = s.gitEnv
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *extractState) revParse(ref string) (string, error) {
	return s.runGit("rev-parse", ref)
}

func (s *extractState) cleanup() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
}

// doExtract calls docker.ExtractResults against the shared worktree with a
// local-cp mock for docker cp (no real container needed).
func (s *extractState) doExtract(baseSHALabel string) (docker.ExtractResult, error) {
	if s.worktree == nil {
		return docker.ExtractResult{}, fmt.Errorf("no worktree prepared")
	}
	baseSHA, ok := s.commits[baseSHALabel]
	if !ok {
		return docker.ExtractResult{}, fmt.Errorf("unknown commit label %q", baseSHALabel)
	}

	// Write a unique file per extraction so git add -A picks up a real change.
	fixtureDir, err := os.MkdirTemp("", "extract-fixture-*")
	if err != nil {
		return docker.ExtractResult{}, err
	}
	defer os.RemoveAll(fixtureDir)

	label := fmt.Sprintf("run%d.txt", len(s.results)+1)
	if err := os.WriteFile(filepath.Join(fixtureDir, label), []byte("content\n"), 0644); err != nil {
		return docker.ExtractResult{}, err
	}

	restore := docker.SetDockerCpFunc(func(_ context.Context, src, dst string) error {
		return exec.Command("cp", "-a", fixtureDir+"/.", dst).Run()
	})
	defer restore()
	restoreExec := docker.SetDockerExecFunc(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(""), nil
	})
	defer restoreExec()

	return docker.ExtractResults(context.Background(), docker.ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  s.worktree.Dir,
		Branch:       s.worktree.Branch,
		BaseSHA:      baseSHA,
		RunID:        fmt.Sprintf("run-%d", len(s.results)+1),
		WorkflowName: "develop",
		Result:       "succeeded",
	})
}

// Step: a git repository with an initial commit labelled "X"
func (s *extractState) gitRepoWithInitialCommit(label string) error {
	dir, err := os.MkdirTemp("", "extract-bdd-repo-*")
	if err != nil {
		return err
	}
	s.cleanups = append(s.cleanups, func() { os.RemoveAll(dir) })
	s.repoDir = dir

	run := func(args ...string) error {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = s.gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cmd %v: %s: %w", args, out, err)
		}
		return nil
	}

	if err := run("git", "init"); err != nil {
		return err
	}
	if err := run("git", "config", "user.email", "test@test.com"); err != nil {
		return err
	}
	if err := run("git", "config", "user.name", "Test User"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		return err
	}
	if err := run("git", "add", "."); err != nil {
		return err
	}
	if err := run("git", "commit", "-m", "initial commit"); err != nil {
		return err
	}

	sha, err := s.revParse("HEAD")
	if err != nil {
		return err
	}
	s.commits[label] = sha
	return nil
}

// Step: an extract worktree prepared from commit "X"
func (s *extractState) worktreePreparedFromCommit(label string) error {
	baseSHA, ok := s.commits[label]
	if !ok {
		return fmt.Errorf("unknown commit label %q", label)
	}
	runID := fmt.Sprintf("wt-%d", len(s.commits))
	targetDir := filepath.Join(s.repoDir, ".gitworktrees", "cloche", runID)
	wt, err := docker.PrepareExtractWorktree(context.Background(), docker.PrepareOptions{
		ProjectDir: s.repoDir,
		BaseSHA:    baseSHA,
		TargetDir:  targetDir,
		Branch:     "cloche/" + runID,
	})
	if err != nil {
		return err
	}
	s.worktree = &wt
	return nil
}

// Step: a sub-workflow has extracted against "X" and produced commit "Y"
func (s *extractState) subWorkflowExtractedProducing(baseSHALabel, resultLabel string) error {
	if s.worktree == nil {
		if err := s.worktreePreparedFromCommit(baseSHALabel); err != nil {
			return err
		}
	}
	result, err := s.doExtract(baseSHALabel)
	if err != nil {
		return err
	}
	s.commits[resultLabel] = result.CommitSHA
	s.results = append(s.results, result)
	return nil
}

// Step: the base branch is advanced to commit "X"
// Simulates the main branch advancing to the given commit (fast-forward).
func (s *extractState) baseBranchAdvancedTo(label string) error {
	sha, ok := s.commits[label]
	if !ok {
		return fmt.Errorf("unknown commit label %q", label)
	}
	// Update the main branch ref directly (works even if it's the current branch
	// in a bare-like setup, since we operate via git update-ref).
	_, err := s.runGit("update-ref", "refs/heads/main", sha)
	if err != nil {
		// Try master if main doesn't exist.
		_, err2 := s.runGit("update-ref", "refs/heads/master", sha)
		if err2 != nil {
			return fmt.Errorf("advancing base branch to %s: %w (also tried master: %v)", sha, err, err2)
		}
	}
	return nil
}

// Step: a sub-workflow extracts its results against commit "X"
func (s *extractState) subWorkflowExtractsAgainst(label string) error {
	result, err := s.doExtract(label)
	s.results = append(s.results, result)
	s.errors = append(s.errors, err)
	return nil
}

// Step: the extraction commit's parent is commit "X"
// (also covers "the new extraction commit's parent is commit X")
func (s *extractState) extractionCommitParentIs(label string) error {
	if len(s.results) == 0 {
		return fmt.Errorf("no extraction results recorded")
	}
	last := s.results[len(s.results)-1]
	if last.CommitSHA == "" {
		return fmt.Errorf("last extraction has no commit SHA")
	}
	wantParent, ok := s.commits[label]
	if !ok {
		return fmt.Errorf("unknown commit label %q", label)
	}
	// git rev-parse <commit>^  gives the parent.
	cmd := exec.Command("git", "rev-parse", last.CommitSHA+"^")
	cmd.Dir = s.repoDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("rev-parse parent of %s: %w", last.CommitSHA, err)
	}
	got := strings.TrimSpace(string(out))
	if got != wantParent {
		return fmt.Errorf("extraction commit %s has parent %s, want %s (%s)", last.CommitSHA, got, wantParent, label)
	}
	return nil
}

// Step: the merge-base of "X" and the extraction commit is "Y"
func (s *extractState) mergeBaseIs(aLabel, bLabel string) error {
	if len(s.results) == 0 {
		return fmt.Errorf("no extraction results recorded")
	}
	last := s.results[len(s.results)-1]
	aSHA, ok := s.commits[aLabel]
	if !ok {
		return fmt.Errorf("unknown commit label %q", aLabel)
	}
	wantMergeBase, ok := s.commits[bLabel]
	if !ok {
		return fmt.Errorf("unknown commit label %q", bLabel)
	}
	cmd := exec.Command("git", "merge-base", aSHA, last.CommitSHA)
	cmd.Dir = s.repoDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git merge-base %s %s: %w", aSHA, last.CommitSHA, err)
	}
	got := strings.TrimSpace(string(out))
	if got != wantMergeBase {
		return fmt.Errorf("merge-base of %s and extraction commit is %s, want %s (%s)", aLabel, got, wantMergeBase, bLabel)
	}
	return nil
}

// Step: the extraction history from "X" to the new commit is linear
func (s *extractState) extractionHistoryIsLinear(fromLabel string) error {
	if len(s.results) == 0 {
		return fmt.Errorf("no extraction results recorded")
	}
	last := s.results[len(s.results)-1]
	fromSHA, ok := s.commits[fromLabel]
	if !ok {
		return fmt.Errorf("unknown commit label %q", fromLabel)
	}
	// Linear means no merges between fromSHA and the tip. Check with --merges.
	cmd := exec.Command("git", "log", "--merges", "--oneline", fromSHA+".."+last.CommitSHA)
	cmd.Dir = s.repoDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git log --merges: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("history from %s to %s is not linear: merge commits found:\n%s", fromLabel, last.CommitSHA, out)
	}
	return nil
}

// Step: an extract worktree prepared from commit "X" that has since advanced with extra commits
func (s *extractState) worktreePreparedAdvanced(label string) error {
	if err := s.worktreePreparedFromCommit(label); err != nil {
		return err
	}
	// Add an extra commit to the worktree, simulating a prior extraction.
	extraFile := filepath.Join(s.worktree.Dir, "extra.txt")
	if err := os.WriteFile(extraFile, []byte("extra\n"), 0644); err != nil {
		return err
	}
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = s.worktree.Dir
	addCmd.Env = s.gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add in worktree: %s: %w", out, err)
	}
	commitCmd := exec.Command("git", "commit", "-m", "prior extraction commit")
	commitCmd.Dir = s.worktree.Dir
	commitCmd.Env = s.gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit in worktree: %s: %w", out, err)
	}
	// Record the worktree's current HEAD as the prior head.
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = s.worktree.Dir
	out, err := headCmd.Output()
	if err != nil {
		return fmt.Errorf("rev-parse HEAD in worktree: %w", err)
	}
	s.priorHead = strings.TrimSpace(string(out))
	return nil
}

// Step: a new extraction runs against commit "X"
func (s *extractState) newExtractionRunsAgainst(label string) error {
	return s.subWorkflowExtractsAgainst(label)
}

// Step: the new commit is not a descendant of the worktree's prior HEAD
func (s *extractState) newCommitNotDescendantOfPriorHEAD() error {
	if len(s.results) == 0 {
		return fmt.Errorf("no extraction results recorded")
	}
	if s.priorHead == "" {
		return fmt.Errorf("no prior HEAD recorded")
	}
	last := s.results[len(s.results)-1]
	// git merge-base --is-ancestor <ancestor> <descendant> exits 0 if ancestor is
	// reachable from descendant.
	cmd := exec.Command("git", "merge-base", "--is-ancestor", s.priorHead, last.CommitSHA)
	cmd.Dir = s.repoDir
	if err := cmd.Run(); err == nil {
		return fmt.Errorf("new commit %s IS a descendant of prior HEAD %s, but should not be", last.CommitSHA, s.priorHead)
	}
	return nil
}

// Step: the base commit does not change between sub-workflows (no-op setup)
func (s *extractState) baseCommitUnchanged() error {
	// Nothing to do — the base commit stays at "base" by default.
	return nil
}

// Step: two sub-workflows extract sequentially against commit "X"
func (s *extractState) twoSubWorkflowsExtract(label string) error {
	if s.worktree == nil {
		if err := s.worktreePreparedFromCommit(label); err != nil {
			return err
		}
	}
	// First extraction.
	r1, err1 := s.doExtract(label)
	s.results = append(s.results, r1)
	s.errors = append(s.errors, err1)
	if err1 != nil {
		return err1
	}
	// Second extraction against the same base.
	r2, err2 := s.doExtract(label)
	s.results = append(s.results, r2)
	s.errors = append(s.errors, err2)
	return err2
}

// Step: both extraction commits have commit "X" as their parent
func (s *extractState) bothExtractionsHaveParent(label string) error {
	if len(s.results) < 2 {
		return fmt.Errorf("need at least 2 results, have %d", len(s.results))
	}
	wantParent, ok := s.commits[label]
	if !ok {
		return fmt.Errorf("unknown commit label %q", label)
	}
	for i, r := range s.results[:2] {
		cmd := exec.Command("git", "rev-parse", r.CommitSHA+"^")
		cmd.Dir = s.repoDir
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("rev-parse parent of result %d (%s): %w", i+1, r.CommitSHA, err)
		}
		got := strings.TrimSpace(string(out))
		if got != wantParent {
			return fmt.Errorf("result %d commit %s has parent %s, want %s (%s)", i+1, r.CommitSHA, got, wantParent, label)
		}
	}
	return nil
}

// Step: neither extraction returns an error
func (s *extractState) neitherExtractionFails() error {
	for i, err := range s.errors {
		if err != nil {
			return fmt.Errorf("extraction %d returned error: %v", i+1, err)
		}
	}
	return nil
}

func initExtractScenarios(ctx *godog.ScenarioContext) {
	s := newExtractState()

	ctx.Before(func(goCtx context.Context, sc *godog.Scenario) (context.Context, error) {
		s.cleanup()
		*s = *newExtractState()
		return goCtx, nil
	})
	ctx.After(func(goCtx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return goCtx, nil
	})

	ctx.Step(`^a git repository with an initial commit labelled "([^"]*)"$`, s.gitRepoWithInitialCommit)
	ctx.Step(`^an extract worktree prepared from commit "([^"]*)"$`, s.worktreePreparedFromCommit)
	ctx.Step(`^a sub-workflow has extracted against "([^"]*)" and produced commit "([^"]*)"$`, s.subWorkflowExtractedProducing)
	ctx.Step(`^the base branch is advanced to commit "([^"]*)"$`, s.baseBranchAdvancedTo)
	ctx.Step(`^a sub-workflow extracts its results against commit "([^"]*)"$`, s.subWorkflowExtractsAgainst)
	ctx.Step(`^the extraction commit's parent is commit "([^"]*)"$`, s.extractionCommitParentIs)
	ctx.Step(`^the new extraction commit's parent is commit "([^"]*)"$`, s.extractionCommitParentIs)
	ctx.Step(`^the merge-base of "([^"]*)" and the extraction commit is "([^"]*)"$`, s.mergeBaseIs)
	ctx.Step(`^the extraction history from "([^"]*)" to the new commit is linear$`, s.extractionHistoryIsLinear)
	ctx.Step(`^an extract worktree prepared from commit "([^"]*)" that has since advanced with extra commits$`, s.worktreePreparedAdvanced)
	ctx.Step(`^a new extraction runs against commit "([^"]*)"$`, s.newExtractionRunsAgainst)
	ctx.Step(`^the new commit is not a descendant of the worktree's prior HEAD$`, s.newCommitNotDescendantOfPriorHEAD)
	ctx.Step(`^the base commit does not change between sub-workflows$`, s.baseCommitUnchanged)
	ctx.Step(`^two sub-workflows extract sequentially against commit "([^"]*)"$`, s.twoSubWorkflowsExtract)
	ctx.Step(`^both extraction commits have commit "([^"]*)" as their parent$`, s.bothExtractionsHaveParent)
	ctx.Step(`^neither extraction returns an error$`, s.neitherExtractionFails)
}
