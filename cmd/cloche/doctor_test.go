package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckResult_statusString verifies status label mapping.
func TestCheckResult_statusString(t *testing.T) {
	tests := []struct {
		status checkStatus
		want   string
	}{
		{checkOK, "ok"},
		{checkWarning, "warning"},
		{checkFail, "FAIL"},
	}
	for _, tt := range tests {
		r := checkResult{status: tt.status}
		if got := r.statusString(); got != tt.want {
			t.Errorf("statusString() = %q, want %q", got, tt.want)
		}
	}
}

// TestCheckDocker_notFound simulates a missing docker binary.
func TestCheckDocker_notFound(t *testing.T) {
	// Override PATH so docker is not found.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", t.TempDir()) // empty directory — no docker binary

	dr := &doctorRunner{}
	result := dr.checkDocker()
	if result.status != checkFail {
		t.Errorf("expected checkFail, got %v", result.status)
	}
	if result.remediation == "" {
		t.Error("expected non-empty remediation")
	}
}

// TestCheckBaseImage_notFound simulates both base images missing.
func TestCheckBaseImage_notFound(t *testing.T) {
	// Override PATH so docker is not found (image inspect will fail).
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", t.TempDir())

	dr := &doctorRunner{}
	result := dr.checkBaseImage()
	if result.status != checkFail {
		t.Errorf("expected checkFail, got %v", result.status)
	}
	if !strings.Contains(result.remediation, "make docker-base") {
		t.Errorf("expected remediation to mention 'make docker-base', got %q", result.remediation)
	}
}

// TestCheckDaemon_unreachable verifies failure when no daemon is listening.
func TestCheckDaemon_unreachable(t *testing.T) {
	dr := &doctorRunner{addr: "127.0.0.1:19999"} // nothing listens here
	result := dr.checkDaemon()
	if result.status != checkFail {
		t.Errorf("expected checkFail, got %v", result.status)
	}
	if result.remediation == "" {
		t.Error("expected non-empty remediation")
	}
}

// TestCheckAuth_apiKeySet verifies OK when ANTHROPIC_API_KEY is present.
func TestCheckAuth_apiKeySet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	dr := &doctorRunner{}
	result := dr.checkAuth()
	if result.status != checkOK {
		t.Errorf("expected checkOK, got %v", result.status)
	}
	if !strings.Contains(result.detail, "ANTHROPIC_API_KEY") {
		t.Errorf("expected detail to mention ANTHROPIC_API_KEY, got %q", result.detail)
	}
}

// TestCheckAuth_claudeDir verifies OK when ~/.claude/ contains session files.
func TestCheckAuth_claudeDir(t *testing.T) {
	// Unset API key so we fall through to directory check.
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Create a fake home with a .claude/credentials file.
	fakeHome := t.TempDir()
	claudeDir := filepath.Join(fakeHome, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "credentials"), []byte("fake"), 0600)

	// Temporarily set HOME so os.UserHomeDir() returns our fake home.
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
	os.Setenv("HOME", fakeHome)

	dr := &doctorRunner{}
	result := dr.checkAuth()
	if result.status != checkOK {
		t.Errorf("expected checkOK with ~/.claude/credentials, got %v (detail: %q)", result.status, result.detail)
	}
}

// TestCheckAuth_noCreds verifies warning when no credentials exist.
func TestCheckAuth_noCreds(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	fakeHome := t.TempDir() // no .claude directory
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
	os.Setenv("HOME", fakeHome)

	dr := &doctorRunner{}
	result := dr.checkAuth()
	if result.status != checkWarning {
		t.Errorf("expected checkWarning, got %v", result.status)
	}
	if result.remediation == "" {
		t.Error("expected non-empty remediation")
	}
}

// TestDoctorRunner_printResults_allPass verifies "All checks passed" is printed.
func TestDoctorRunner_printResults_allPass(t *testing.T) {
	// Redirect stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	dr := &doctorRunner{}
	dr.printResults([]checkResult{
		{label: "Checking foo", status: checkOK},
		{label: "Checking bar", status: checkOK, detail: "v1.2.3"},
	})

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf.Write(tmp[:n])
		if err != nil {
			break
		}
	}
	output := buf.String()

	if !strings.Contains(output, "All checks passed.") {
		t.Errorf("expected 'All checks passed.' in output, got:\n%s", output)
	}
}

// TestDoctorRunner_printResults_withFailure verifies failure output.
func TestDoctorRunner_printResults_withFailure(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	dr := &doctorRunner{}
	dr.printResults([]checkResult{
		{label: "Checking foo", status: checkFail, remediation: "Fix it with: foo --repair"},
	})

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf.Write(tmp[:n])
		if err != nil {
			break
		}
	}
	output := buf.String()

	if !strings.Contains(output, "FAIL") {
		t.Errorf("expected FAIL in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Fix it with: foo --repair") {
		t.Errorf("expected remediation in output, got:\n%s", output)
	}
}
