package qaconformance

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StubModuleFS is the stub qa module: module.toml, qa.cloche, scripts and the
// qa-stub tool. It is the reference for what a backend module provides.
//
//go:embed stubmodule
var StubModuleFS embed.FS

// StubBackend drives the stub module's scripts with `cloche get|set` replaced
// by a file-backed kv, so the contract runs without a daemon.
type StubBackend struct {
	ModuleDir string // where the stub module was installed
	state     string
	kv        string
}

// InstallStubModule writes the stub module into dir/<name> (executable bits
// set) and returns its path.
func InstallStubModule(dir string) (string, error) {
	root := filepath.Join(dir, "stub")
	err := fs.WalkDir(StubModuleFS, "stubmodule", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, strings.TrimPrefix(p, "stubmodule"))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := StubModuleFS.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") || filepath.Base(p) == "qa-stub" || filepath.Base(p) == "cloche-kv" {
			mode = 0o755
		}
		return os.WriteFile(dst, data, mode)
	})
	return root, err
}

// NewStubBackend installs the stub module under a temp dir and returns it as a Backend.
func NewStubBackend() (*StubBackend, error) {
	dir, err := os.MkdirTemp("", "qa-stub-")
	if err != nil {
		return nil, err
	}
	mod, err := InstallStubModule(dir)
	if err != nil {
		return nil, err
	}
	// bin/cloche is the kv stand-in; bin/qa-stub the tool.
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, err
	}
	for src, name := range map[string]string{"cloche-kv": "cloche", "qa-stub": "qa-stub"} {
		if err := os.Symlink(filepath.Join(mod, src), filepath.Join(bin, name)); err != nil {
			return nil, err
		}
	}
	return &StubBackend{ModuleDir: mod, state: filepath.Join(dir, "state"), kv: filepath.Join(dir, "kv")}, nil
}

// Close removes the stub's temp dir.
func (s *StubBackend) Close() { os.RemoveAll(filepath.Dir(s.ModuleDir)) }

func (s *StubBackend) env() []string {
	return append(os.Environ(),
		"PATH="+filepath.Join(filepath.Dir(s.ModuleDir), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"QA_STUB_DIR="+s.state, "KV_DIR="+s.kv, "CLOCHE_MODULE_DIR="+s.ModuleDir)
}

// phase runs scripts/<script> with the kv inputs and returns the kv outputs.
func (s *StubBackend) phase(script string, in map[string]string) (map[string]string, error) {
	os.RemoveAll(s.kv)
	if err := os.MkdirAll(s.kv, 0o755); err != nil {
		return nil, err
	}
	for k, v := range in {
		if err := os.WriteFile(filepath.Join(s.kv, k), []byte(v), 0o644); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command("bash", filepath.Join(s.ModuleDir, "scripts", script))
	cmd.Env = s.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", script, err, strings.TrimSpace(string(out)))
	}
	out := map[string]string{}
	entries, err := os.ReadDir(s.kv)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(s.kv, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = string(b)
	}
	return out, nil
}

func (s *StubBackend) Ask(file string) ([]string, error) {
	out, err := s.phase("ask.sh", map[string]string{"questions_file": file})
	if err != nil {
		return nil, err
	}
	return strings.Fields(out["question_ids"]), nil
}

func (s *StubBackend) Open(ids []string) ([]string, error) {
	out, err := s.phase("await.sh", map[string]string{"question_ids": strings.Join(ids, "\n")})
	if err != nil {
		return nil, err
	}
	return strings.Fields(out["questions_open"]), nil
}

func (s *StubBackend) Read(id string) (string, error) {
	out, err := s.phase("read.sh", map[string]string{"question_id": id})
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(out["answer_path"])
	return string(data), err
}

func (s *StubBackend) Answer(id, who, text string) error {
	cmd := exec.Command(filepath.Join(s.ModuleDir, "qa-stub"), "answer", id, who, text)
	cmd.Env = s.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qa-stub answer: %v: %s", err, out)
	}
	return nil
}
