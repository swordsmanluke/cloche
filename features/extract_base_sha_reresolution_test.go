package features_test

import (
	"errors"
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// InitializeScenario binds Gherkin steps to pending stub functions.
// All stubs return an error so the suite fails loudly until implementation lands.
func InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.Step(`^a git repository with an initial commit labelled "([^"]*)"$`, gitRepoWithInitialCommit)
	ctx.Step(`^an extract worktree prepared from commit "([^"]*)"$`, worktreePreparedFromCommit)
	ctx.Step(`^a sub-workflow has extracted against "([^"]*)" and produced commit "([^"]*)"$`, subWorkflowExtractedProducing)
	ctx.Step(`^the base branch is advanced to commit "([^"]*)"$`, baseBranchAdvancedTo)
	ctx.Step(`^a sub-workflow extracts its results against commit "([^"]*)"$`, subWorkflowExtractsAgainst)
	ctx.Step(`^the extraction commit's parent is commit "([^"]*)"$`, extractionCommitParentIs)
	ctx.Step(`^the merge-base of "([^"]*)" and the extraction commit is "([^"]*)"$`, mergeBaseIs)
	ctx.Step(`^the extraction history from "([^"]*)" to the new commit is linear$`, extractionHistoryIsLinear)
	ctx.Step(`^an extract worktree prepared from commit "([^"]*)" that has since advanced with extra commits$`, worktreePreparedAdvanced)
	ctx.Step(`^a new extraction runs against commit "([^"]*)"$`, newExtractionRunsAgainst)
	ctx.Step(`^the new commit is not a descendant of the worktree's prior HEAD$`, newCommitNotDescendantOfPriorHEAD)
	ctx.Step(`^the base commit does not change between sub-workflows$`, baseCommitUnchanged)
	ctx.Step(`^two sub-workflows extract sequentially against commit "([^"]*)"$`, twoSubWorkflowsExtract)
	ctx.Step(`^both extraction commits have commit "([^"]*)" as their parent$`, bothExtractionsHaveParent)
	ctx.Step(`^neither extraction returns an error$`, neitherExtractionFails)
}

func gitRepoWithInitialCommit(_ string) error {
	return errors.New("pending: L1 implementation")
}

func worktreePreparedFromCommit(_ string) error {
	return errors.New("pending: L1 implementation")
}

func subWorkflowExtractedProducing(_, _ string) error {
	return errors.New("pending: L1 implementation")
}

func baseBranchAdvancedTo(_ string) error {
	return errors.New("pending: L1 implementation")
}

func subWorkflowExtractsAgainst(_ string) error {
	return errors.New("pending: L1 implementation")
}

func extractionCommitParentIs(_ string) error {
	return errors.New("pending: L1 implementation")
}

func mergeBaseIs(_, _ string) error {
	return errors.New("pending: L1 implementation")
}

func extractionHistoryIsLinear(_ string) error {
	return errors.New("pending: L1 implementation")
}

func worktreePreparedAdvanced(_ string) error {
	return errors.New("pending: L1 implementation")
}

func newExtractionRunsAgainst(_ string) error {
	return errors.New("pending: L1 implementation")
}

func newCommitNotDescendantOfPriorHEAD() error {
	return errors.New("pending: L1 implementation")
}

func baseCommitUnchanged() error {
	return errors.New("pending: L1 implementation")
}

func twoSubWorkflowsExtract(_ string) error {
	return errors.New("pending: L1 implementation")
}

func bothExtractionsHaveParent(_ string) error {
	return errors.New("pending: L1 implementation")
}

func neitherExtractionFails() error {
	return errors.New("pending: L1 implementation")
}

func TestMain(m *testing.M) {
	opts := godog.Options{
		Format: "pretty",
		Paths:  []string{"."},
	}

	status := godog.TestSuite{
		Name:                "extract-base-sha-reresolution",
		ScenarioInitializer: InitializeScenario,
		Options:             &opts,
	}.Run()

	if st := m.Run(); st > status {
		status = st
	}

	os.Exit(status)
}
