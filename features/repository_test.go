package features_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/project"
	"github.com/cloche-dev/cloche/internal/projectcli"
	"github.com/cucumber/godog"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── shared scenario state ────────────────────────────────────────────────────

type repositoryCtx struct {
	// config.toml scenarios
	configContent  string
	parsedConfig   *config.Config
	configParseErr error

	// DSL scenarios
	dslContent      string
	parsedWorkflows map[string]*domain.Workflow
	dslParseErr     error

	// CLI / daemon integration (L2)
	projectDir    string           // temp project root for CLI tests
	daemonAddr    string           // address of the in-process test gRPC server
	daemonServer  *grpclib.Server  // in-process server (stopped on reset)
	commandOutput string           // captured output from last CLI command
	commandErr    error            // error returned by last CLI command

	// Auto-seeding (L3 Scenario 5)
	autoSeedDir   string          // temp project root for auto-seed scenario
	autoSeedStore *sqlite.Store   // in-process SQLite store for auto-seed scenario
	seededRepos   []*domain.Repository
}

func (s *repositoryCtx) reset() {
	if s.daemonServer != nil {
		s.daemonServer.Stop()
	}
	if s.projectDir != "" {
		os.RemoveAll(s.projectDir)
	}
	if s.autoSeedStore != nil {
		s.autoSeedStore.Close()
	}
	if s.autoSeedDir != "" {
		os.RemoveAll(s.autoSeedDir)
	}
	*s = repositoryCtx{}
}

// ─── in-process test gRPC server ─────────────────────────────────────────────

// testProjectServer implements only GetProjectInfo, backed by project.Load.
type testProjectServer struct {
	pb.UnimplementedClocheServiceServer
	projectDir string
}

func (t *testProjectServer) GetProjectInfo(_ context.Context, req *pb.GetProjectInfoRequest) (*pb.GetProjectInfoResponse, error) {
	dir := t.projectDir
	if req.ProjectDir != "" {
		dir = req.ProjectDir
	}

	proj, err := project.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("loading project: %w", err)
	}
	cfg, _ := config.Load(dir)

	var pbRepos []*pb.Repository
	for _, r := range proj.Repositories {
		pbRepos = append(pbRepos, &pb.Repository{
			Name: r.Name,
			Path: r.Path,
		})
	}

	active := false
	if cfg != nil {
		active = cfg.Active
	}

	return &pb.GetProjectInfoResponse{
		ProjectDir:   dir,
		Name:         filepath.Base(dir),
		Active:       active,
		Repositories: pbRepos,
	}, nil
}

func startTestDaemon(projectDir string) (addr string, srv *grpclib.Server, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	srv = grpclib.NewServer()
	pb.RegisterClocheServiceServer(srv, &testProjectServer{projectDir: projectDir})
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), srv, nil
}

// ─── step registrations ──────────────────────────────────────────────────────

func initRepositoryScenarios(ctx *godog.ScenarioContext) {
	s := &repositoryCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background (CLI feature — L2)
	ctx.Step(`^the daemon is running against a test project directory$`, s.theDaemonIsRunning)

	// DSL parsing
	ctx.Step(`^a \.cloche file containing:$`, s.aClocheFileContaining)
	ctx.Step(`^the DSL parser processes the file$`, s.theDSLParserProcessesTheFile)
	ctx.Step(`^no parse error is returned$`, s.noParsedError)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has repository "([^"]*)"$`, s.stepHasRepository)
	ctx.Step(`^workflow "([^"]*)" declares repos \[([^\]]*)\]$`, s.workflowDeclaredRepos)
	ctx.Step(`^workflow "([^"]*)" declares (\d+) repos$`, s.workflowRepoCount)

	// Config.toml parsing
	ctx.Step(`^a config\.toml containing:$`, s.aConfigTOMLContaining)
	ctx.Step(`^a config\.toml containing no repository entries$`, s.aConfigTOMLContainingNoRepos)
	ctx.Step(`^the config is parsed$`, s.theConfigIsParsed)
	ctx.Step(`^the config contains a repository named "([^"]*)" with path "([^"]*)"$`, s.configContainsRepoWithPath)
	ctx.Step(`^the single-entry config has an implicit default repository named "([^"]*)"$`, s.singleEntryImplicitDefault)
	ctx.Step(`^the config contains a repository named "([^"]*)"$`, s.configContainsRepoByName)
	ctx.Step(`^the config contains (\d+) repositor(?:y|ies)$`, s.configContainsRepoCount)

	// CLI project display (L2)
	ctx.Step(`^the project's config\.toml declares:$`, s.theProjectConfigTOMLDeclares)
	ctx.Step(`^the project's config\.toml has no repository entries$`, s.theProjectConfigTOMLHasNoRepos)
	ctx.Step(`^the project's config\.toml has a repository entry named "([^"]*)" with path "([^"]*)"$`, s.theProjectConfigTOMLHasRepo)
	ctx.Step(`^the user runs "([^"]*)"$`, s.theUserRunsCommand)
	ctx.Step(`^the command succeeds$`, s.theCommandSucceeds)
	ctx.Step(`^the output contains "([^"]*)"$`, s.theOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, s.theOutputNotContains)
	ctx.Step(`^the output contains a deprecation warning about missing repository configuration$`, s.outputContainsDeprecationWarning)
	ctx.Step(`^the output contains migration instructions for adding repository configuration$`, s.outputContainsMigrationInstructions)

	// Backward compatibility (L2)
	ctx.Step(`^the project has no stored repositories$`, s.theProjectHasNoStoredRepos)

	// Auto-seeding (L3)
	ctx.Step(`^a project database that has been freshly migrated with no repository rows$`, s.freshMigration)
	ctx.Step(`^the repositories store is first accessed for that project$`, s.firstAccess)
	ctx.Step(`^exactly (\d+) repositor(?:y|ies) (?:is|are) seeded automatically$`, s.seededCount)
	ctx.Step(`^the seeded repository has path equal to the project root directory$`, s.seededPath)
}

// ─── CLI daemon integration steps (L2) ───────────────────────────────────────

func (s *repositoryCtx) theDaemonIsRunning() error {
	dir, err := os.MkdirTemp("", "cloche-bdd-*")
	if err != nil {
		return fmt.Errorf("creating temp project dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cloche"), 0755); err != nil {
		os.RemoveAll(dir)
		return err
	}
	s.projectDir = dir

	addr, srv, err := startTestDaemon(dir)
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("starting test daemon: %w", err)
	}
	s.daemonAddr = addr
	s.daemonServer = srv
	return nil
}

func (s *repositoryCtx) theProjectConfigTOMLDeclares(content *godog.DocString) error {
	if s.projectDir == "" {
		return fmt.Errorf("daemon not started; call 'the daemon is running' first")
	}
	return os.WriteFile(filepath.Join(s.projectDir, ".cloche", "config.toml"), []byte(content.Content), 0644)
}

func (s *repositoryCtx) theProjectConfigTOMLHasNoRepos() error {
	if s.projectDir == "" {
		return fmt.Errorf("daemon not started; call 'the daemon is running' first")
	}
	return os.WriteFile(filepath.Join(s.projectDir, ".cloche", "config.toml"), []byte(""), 0644)
}

func (s *repositoryCtx) theProjectConfigTOMLHasRepo(name, repoPath string) error {
	if s.projectDir == "" {
		return fmt.Errorf("daemon not started; call 'the daemon is running' first")
	}
	content := fmt.Sprintf("[[repositories]]\nname = %q\npath = %q\n", name, repoPath)
	return os.WriteFile(filepath.Join(s.projectDir, ".cloche", "config.toml"), []byte(content), 0644)
}

func (s *repositoryCtx) theUserRunsCommand(cmd string) error {
	if s.daemonAddr == "" {
		return fmt.Errorf("daemon not started")
	}

	conn, err := grpclib.NewClient(s.daemonAddr, grpclib.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		s.commandErr = fmt.Errorf("connecting: %w", err)
		return nil
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.GetProjectInfo(ctx, &pb.GetProjectInfoRequest{ProjectDir: s.projectDir})
	if err != nil {
		s.commandErr = err
		return nil
	}

	var buf bytes.Buffer
	parts := strings.Fields(cmd)

	// Dispatch based on command: "cloche project repos list" vs "cloche project"
	if len(parts) >= 4 && parts[0] == "cloche" && parts[1] == "project" && parts[2] == "repos" && parts[3] == "list" {
		projectcli.WriteReposList(resp.Repositories, &buf)
	} else {
		bddWriteProjectInfo(resp, &buf)
	}

	s.commandOutput = buf.String()
	s.commandErr = nil
	return nil
}

func (s *repositoryCtx) theCommandSucceeds() error {
	if s.commandErr != nil {
		return fmt.Errorf("command failed: %v", s.commandErr)
	}
	return nil
}

func (s *repositoryCtx) theOutputContains(text string) error {
	if !strings.Contains(s.commandOutput, text) {
		return fmt.Errorf("output does not contain %q\nfull output:\n%s", text, s.commandOutput)
	}
	return nil
}

func (s *repositoryCtx) theOutputNotContains(text string) error {
	if strings.Contains(s.commandOutput, text) {
		return fmt.Errorf("output unexpectedly contains %q\nfull output:\n%s", text, s.commandOutput)
	}
	return nil
}

func (s *repositoryCtx) theProjectHasNoStoredRepos() error {
	// No-op for L2: there is no repository store. Repositories come solely from config.toml.
	return nil
}

// bddWriteProjectInfo mirrors printProjectInfo in cmd/cloche/project.go.
func bddWriteProjectInfo(resp *pb.GetProjectInfoResponse, w *bytes.Buffer) {
	fmt.Fprintf(w, "Project:     %s\n", resp.Name)
	fmt.Fprintf(w, "Directory:   %s\n", resp.ProjectDir)

	if len(resp.Repositories) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Repositories:")
		for _, repo := range resp.Repositories {
			if repo.Url != "" {
				fmt.Fprintf(w, "  %-20s  %-30s  %s\n", repo.Name, repo.Path, repo.Url)
			} else {
				fmt.Fprintf(w, "  %-20s  %s\n", repo.Name, repo.Path)
			}
		}
	} else {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "DEPRECATED: No repository configuration found in .cloche/config.toml.\n")
		fmt.Fprintf(w, "  To configure repositories, add a [[repositories]] section:\n")
		fmt.Fprintf(w, "    [[repositories]]\n")
		fmt.Fprintf(w, "    name = \"main\"\n")
		fmt.Fprintf(w, "    path = \".\"\n")
	}
}

// ─── DSL implementations (L1) ────────────────────────────────────────────────

func (s *repositoryCtx) aClocheFileContaining(content *godog.DocString) error {
	s.dslContent = content.Content
	return nil
}

func (s *repositoryCtx) theDSLParserProcessesTheFile() error {
	workflows, err := dsl.ParseAll(s.dslContent)
	s.parsedWorkflows = workflows
	s.dslParseErr = err
	return nil
}

func (s *repositoryCtx) noParsedError() error {
	if s.configParseErr != nil {
		return fmt.Errorf("unexpected config parse error: %w", s.configParseErr)
	}
	if s.dslParseErr != nil {
		return fmt.Errorf("unexpected DSL parse error: %w", s.dslParseErr)
	}
	return nil
}

func (s *repositoryCtx) stepHasRepository(stepName, workflowName, repoName string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	step, ok := wf.Steps[stepName]
	if !ok {
		return fmt.Errorf("step %q not found in workflow %q", stepName, workflowName)
	}
	got, ok := step.Config["repository"]
	if !ok {
		return fmt.Errorf("step %q has no repository field", stepName)
	}
	if got != repoName {
		return fmt.Errorf("step %q: expected repository %q, got %q", stepName, repoName, got)
	}
	return nil
}

func (s *repositoryCtx) workflowDeclaredRepos(workflowName, reposList string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	expected := parseQuotedStringList(reposList)
	if len(wf.Repos) != len(expected) {
		return fmt.Errorf("workflow %q: expected repos %v, got %v", workflowName, expected, wf.Repos)
	}
	for i, e := range expected {
		if wf.Repos[i] != e {
			return fmt.Errorf("workflow %q repos[%d]: expected %q, got %q", workflowName, i, e, wf.Repos[i])
		}
	}
	return nil
}

func (s *repositoryCtx) workflowRepoCount(workflowName string, count int) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	if len(wf.Repos) != count {
		return fmt.Errorf("workflow %q: expected %d repos, got %d", workflowName, count, len(wf.Repos))
	}
	return nil
}

// parseQuotedStringList parses `"a"` or `"a", "b"` into []string.
func parseQuotedStringList(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, `"`)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// ─── Config.toml implementations (L1) ────────────────────────────────────────

func (s *repositoryCtx) aConfigTOMLContaining(content *godog.DocString) error {
	s.configContent = content.Content
	return nil
}

func (s *repositoryCtx) aConfigTOMLContainingNoRepos() error {
	s.configContent = ""
	return nil
}

func (s *repositoryCtx) theConfigIsParsed() error {
	tmpDir, err := os.MkdirTemp("", "cloche-config-test-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	clocheDir := filepath.Join(tmpDir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0755); err != nil {
		return fmt.Errorf("creating .cloche dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(s.configContent), 0644); err != nil {
		return fmt.Errorf("writing config.toml: %w", err)
	}

	cfg, err := config.Load(tmpDir)
	s.parsedConfig = cfg
	s.configParseErr = err
	return nil
}

func (s *repositoryCtx) configContainsRepoByName(name string) error {
	for _, r := range s.parsedConfig.Repositories {
		if r.Name == name {
			return nil
		}
	}
	return fmt.Errorf("no repository named %q in config", name)
}

func (s *repositoryCtx) configContainsRepoWithPath(name, path string) error {
	for _, r := range s.parsedConfig.Repositories {
		if r.Name == name && r.Path == path {
			return nil
		}
	}
	return fmt.Errorf("no repository named %q with path %q in config", name, path)
}

// singleEntryImplicitDefault verifies that a single-entry config resolves an implicit default.
// With exactly one [[repositories]] entry there is no need for a "default" field — the sole
// entry is the default by definition.
func (s *repositoryCtx) singleEntryImplicitDefault(name string) error {
	if len(s.parsedConfig.Repositories) != 1 {
		return fmt.Errorf("expected exactly 1 repository for implicit default, got %d", len(s.parsedConfig.Repositories))
	}
	if s.parsedConfig.Repositories[0].Name != name {
		return fmt.Errorf("implicit default repository: expected %q, got %q", name, s.parsedConfig.Repositories[0].Name)
	}
	return nil
}

func (s *repositoryCtx) configContainsRepoCount(count int) error {
	if len(s.parsedConfig.Repositories) != count {
		return fmt.Errorf("expected %d repositories, got %d", count, len(s.parsedConfig.Repositories))
	}
	return nil
}

// ─── L3 implementations ──────────────────────────────────────────────────────

// Deprecation warning: emitted by bddWriteProjectInfo when no repos are configured.
const (
	deprecationWarningText    = "DEPRECATED:"
	migrationInstructionsText = "[[repositories]]"
)

func (s *repositoryCtx) outputContainsDeprecationWarning() error {
	if !strings.Contains(s.commandOutput, deprecationWarningText) {
		return fmt.Errorf("output does not contain deprecation warning %q\nfull output:\n%s", deprecationWarningText, s.commandOutput)
	}
	return nil
}

func (s *repositoryCtx) outputContainsMigrationInstructions() error {
	if !strings.Contains(s.commandOutput, migrationInstructionsText) {
		return fmt.Errorf("output does not contain migration instructions %q\nfull output:\n%s", migrationInstructionsText, s.commandOutput)
	}
	return nil
}

// Auto-seeding scenario steps.

func (s *repositoryCtx) freshMigration() error {
	dir, err := os.MkdirTemp("", "cloche-autoseed-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	dsn := filepath.Join(dir, "cloche.db")
	store, err := sqlite.NewStore(dsn)
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("creating store: %w", err)
	}
	s.autoSeedDir = dir
	s.autoSeedStore = store
	return nil
}

func (s *repositoryCtx) firstAccess() error {
	if s.autoSeedStore == nil {
		return fmt.Errorf("no store; call 'a project database that has been freshly migrated' first")
	}
	repos, err := s.autoSeedStore.ListRepositories(context.Background(), s.autoSeedDir)
	if err != nil {
		return fmt.Errorf("listing repositories: %w", err)
	}
	s.seededRepos = repos
	return nil
}

func (s *repositoryCtx) seededCount(count int) error {
	if len(s.seededRepos) != count {
		return fmt.Errorf("expected %d seeded repositories, got %d", count, len(s.seededRepos))
	}
	return nil
}

func (s *repositoryCtx) seededPath() error {
	if len(s.seededRepos) == 0 {
		return fmt.Errorf("no seeded repositories")
	}
	if s.seededRepos[0].Path != s.autoSeedDir {
		return fmt.Errorf("seeded repository path: expected %q, got %q", s.autoSeedDir, s.seededRepos[0].Path)
	}
	return nil
}

// ─── TestMain ────────────────────────────────────────────────────────────────

func TestMain(m *testing.M) {
	opts := godog.Options{
		Format: "pretty",
		Paths:  []string{"."},
	}

	status := godog.TestSuite{
		Name: "cloche",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			initRepositoryScenarios(ctx)
			initPromptTemplatingScenarios(ctx)
			initTokenLimitScenarios(ctx)
			initVerticalDesignPrepScenarios(ctx)
			initResumeRebuildScenarios(ctx)
		},
		Options: &opts,
	}.Run()

	if st := m.Run(); st > status {
		status = st
	}
	os.Exit(status)
}
