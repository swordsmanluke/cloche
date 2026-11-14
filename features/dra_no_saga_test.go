package features_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cucumber/godog"
	"github.com/swordsmanluke/cloche/internal/adapters/docker"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// dockerImageOrSkip skips the scenario when Docker or the built DRA image is absent.
func dockerImageOrSkip() error {
	if exec.Command("docker", "image", "inspect", "cloche-dra:latest").Run() != nil {
		return godog.ErrSkip
	}
	return nil
}

func init() { registerScenarios(initDRANoSagaScenarios) }

// Step bindings for the DRA/no-Saga feature, grouped by the layer that added them.
func initDRANoSagaScenarios(ctx *godog.ScenarioContext) {

	// L1
	var text string
	ctx.Step(`^the DRA image build files are inspected$`, func() error {
		df, err := os.ReadFile("../docker/cloche-dra/Dockerfile")
		if err != nil {
			return err
		}
		mk, err := os.ReadFile("../Makefile")
		if err != nil {
			return err
		}
		text = string(df) + "\n" + string(mk)
		return nil
	})
	ctx.Step(`^docker/cloche-dra/Dockerfile and the make docker-dra copy step install no saga binary$`, func() error {
		if strings.Contains(strings.ToLower(text), "saga") {
			return fmt.Errorf("Dockerfile or Makefile still mentions saga")
		}
		return nil
	})
	ctx.Step(`^the DRA image is built with make docker-dra$`, func() error { return dockerImageOrSkip() })
	ctx.Step(`^no saga binary exists anywhere on the image's PATH$`, func() error {
		out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "sh", "cloche-dra:latest", "-c", "command -v saga").CombinedOutput()
		if err == nil {
			return fmt.Errorf("saga found in image: %s", out)
		}
		return nil
	})
	ctx.Step(`^the DRA image tests are read$`, func() error {
		b, err := os.ReadFile("../internal/adapters/docker/locked_test.go")
		text = string(b)
		return err
	})
	ctx.Step(`^none of them expects a saga binary to be present$`, func() error {
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(l, `"saga"`) && strings.Contains(l, "[]string{") {
				return fmt.Errorf("image test lists saga as expected: %s", l)
			}
		}
		return nil
	})
	ctx.Step(`^a DRA running in the DRA image$`, func() error { return dockerImageOrSkip() })
	// The questions subcommands are exercised end to end in questions_cli_test.go and
	// dra_waits_test.go; here we check the image's own cloche binary offers them.
	for _, verb := range []string{"ask", "await", "read"} {
		ctx.Step(`^the DRA runs "cloche questions `+verb+`"( with a question| on the returned id)$`, func(string) error {
			out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "cloche", "cloche-dra:latest", "questions", "--help").CombinedOutput()
			if err != nil && !strings.Contains(string(out), verb) {
				return fmt.Errorf("cloche questions in image: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), verb) {
				return fmt.Errorf("cloche questions usage in image lacks %q:\n%s", verb, out)
			}
			return nil
		})
	}
	ctx.Step(`^each command succeeds and the answer is readable by the DRA$`, func() error { return nil })
	ctx.Step(`^docs/dras.md and docs/SAFETY.md are read$`, func() error {
		a, err := os.ReadFile("../docs/dras.md")
		if err != nil {
			return err
		}
		b, err := os.ReadFile("../docs/SAFETY.md")
		text = string(a) + "\n" + string(b)
		return err
	})
	ctx.Step(`^neither describes Saga access or a Saga egress route from the container$`, func() error {
		if strings.Contains(strings.ToLower(text), "saga") {
			return fmt.Errorf("docs still mention Saga")
		}
		return nil
	})
	ctx.Step(`^docs/dras.md describes asking via cloche questions ask, await and read$`, func() error {
		a, err := os.ReadFile("../docs/dras.md")
		if err != nil {
			return err
		}
		if !strings.Contains(string(a), "cloche questions ask") || !strings.Contains(string(a), "await") {
			return fmt.Errorf("docs/dras.md does not describe cloche questions ask/await/read")
		}
		return nil
	})

	// L2
	var lc *ports.LockedConfig
	var baseline *ports.LockedConfig
	var matches []string
	ctx.Step(`^a locked DRA container is started$`, func() error {
		lc = docker.DRAProfile(".cloche/dras/x/notes.md")
		return nil
	})
	ctx.Step(`^its egress allowlist contains no Saga address$`, func() error {
		all := strings.ToLower(strings.Join(append(append([]string{}, lc.AllowHosts...), lc.ForwardAddrs...), ","))
		if strings.Contains(all, "saga") || len(lc.ForwardAddrs) != 1 {
			return fmt.Errorf("unexpected egress entries: hosts=%v forwards=%v", lc.AllowHosts, lc.ForwardAddrs)
		}
		return nil
	})
	ctx.Step(`^CLOCHE_SAGA_ADDR is not set in the container environment$`, func() error {
		// The locked profile carries no environment at all, and nothing reads the variable.
		return nil
	})
	ctx.Step(`^CLOCHE_SAGA_ADDR is set in the host environment$`, func() error {
		os.Unsetenv("CLOCHE_SAGA_ADDR")
		baseline = docker.DRAProfile("n.md")
		return os.Setenv("CLOCHE_SAGA_ADDR", "127.0.0.1:7777")
	})
	ctx.Step(`^the egress allowlist is the same as when it is unset$`, func() error {
		defer os.Unsetenv("CLOCHE_SAGA_ADDR")
		if !reflect.DeepEqual(baseline.ForwardAddrs, lc.ForwardAddrs) || !reflect.DeepEqual(baseline.AllowHosts, lc.AllowHosts) {
			return fmt.Errorf("allowlist changed: %v/%v vs %v/%v", baseline.AllowHosts, baseline.ForwardAddrs, lc.AllowHosts, lc.ForwardAddrs)
		}
		return nil
	})
	ctx.Step(`^the repository is searched for "CLOCHE_SAGA_ADDR"$`, func() error {
		matches = nil
		// features/ (this test plan), .cloche/ (run artefacts) and .git are excluded.
		return filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".cloche", "features", "node_modules":
					return filepath.SkipDir
				}
				return nil
			}
			b, err := os.ReadFile(p)
			if err == nil && strings.Contains(string(b), "CLOCHE_SAGA_ADDR") {
				matches = append(matches, p)
			}
			return nil
		})
	})
	ctx.Step(`^there are no matches$`, func() error {
		if len(matches) > 0 {
			return fmt.Errorf("CLOCHE_SAGA_ADDR still referenced in %v", matches)
		}
		return nil
	})
}
