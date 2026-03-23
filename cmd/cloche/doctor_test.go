package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestCheckProjectConfig_noCloche verifies OK when there is no .cloche dir
// (the function itself should not be called in that case, but it handles it).
func TestCheckProjectConfig_missingConfigToml(t *testing.T) {
	dir := t.TempDir()
	// Create .cloche dir but no config.toml — should succeed with defaults.
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkProjectConfig()
	// No config.toml means defaults; active=false is a warning.
	if result.status == checkFail {
		t.Errorf("expected OK or warning when config.toml absent, got FAIL: %s", result.detail)
	}
}

// TestCheckProjectConfig_activeTrue verifies OK when active=true and image set.
func TestCheckProjectConfig_activeTrue(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
[daemon]
image = "myproject-cloche-agent:latest"
`), 0644)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkProjectConfig()
	if result.status != checkOK {
		t.Errorf("expected checkOK, got %v (detail: %q)", result.status, result.detail)
	}
}

// TestCheckProjectConfig_todoMarkers verifies warning when TODO(cloche-init) markers present.
func TestCheckProjectConfig_todoMarkers(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
[daemon]
image = "myproject-cloche-agent:latest"
`), 0644)
	// Write a workflow file with a TODO marker.
	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(`
workflow "develop" {
  step test {
    # TODO(cloche-init): Replace with your test command
    run     = "echo test"
    results = [success]
  }
  test:success -> done
}
`), 0644)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkProjectConfig()
	if result.status != checkWarning {
		t.Errorf("expected checkWarning for TODO markers, got %v (detail: %q)", result.status, result.detail)
	}
	if !strings.Contains(result.detail, "TODO(cloche-init)") {
		t.Errorf("expected detail to mention TODO(cloche-init), got %q", result.detail)
	}
}

// TestCheckProjectConfig_parseError verifies FAIL on malformed config.toml.
func TestCheckProjectConfig_parseError(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`not valid toml [[[[`), 0644)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkProjectConfig()
	if result.status != checkFail {
		t.Errorf("expected checkFail for parse error, got %v", result.status)
	}
}

// TestCheckWorkflows_valid verifies OK on a valid workflow file.
func TestCheckWorkflows_valid(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(`workflow "develop" {
  step build {
    run     = "echo building"
    results = [success, fail]
  }
  build:success -> done
  build:fail    -> abort
}
`), 0644)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkWorkflows()
	if result.status != checkOK {
		t.Errorf("expected checkOK, got %v (detail: %q, remediation: %q)", result.status, result.detail, result.remediation)
	}
}

// TestCheckWorkflows_syntaxError verifies FAIL on a malformed workflow file.
func TestCheckWorkflows_syntaxError(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "bad.cloche"), []byte(`this is not valid cloche DSL {{{`), 0644)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkWorkflows()
	if result.status != checkFail {
		t.Errorf("expected checkFail for syntax error, got %v", result.status)
	}
	if result.remediation == "" {
		t.Error("expected non-empty remediation with error details")
	}
}

// TestCheckImageBuild_noDockerfile verifies FAIL when Dockerfile is missing.
func TestCheckImageBuild_noDockerfile(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	// No Dockerfile created.
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
[daemon]
image = "test-image:latest"
`), 0644)

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkImageBuild()
	if result.status != checkFail {
		t.Errorf("expected checkFail when Dockerfile missing, got %v", result.status)
	}
	if !strings.Contains(result.detail, "Dockerfile") {
		t.Errorf("expected detail to mention Dockerfile, got %q", result.detail)
	}
}

// TestCheckImageBuild_dockerNotFound verifies FAIL when docker binary is missing.
func TestCheckImageBuild_dockerNotFound(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "Dockerfile"), []byte("FROM scratch\n"), 0644)
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
[daemon]
image = "test-image:latest"
`), 0644)

	// Remove docker from PATH.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", t.TempDir())

	dr := &doctorRunner{projectDir: dir, timeout: 60 * time.Second}
	result := dr.checkImageBuild()
	if result.status != checkFail {
		t.Errorf("expected checkFail when docker not found, got %v", result.status)
	}
}

// TestCheckAgentRoundtrip_dockerNotFound verifies FAIL when docker binary is missing.
func TestCheckAgentRoundtrip_dockerNotFound(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
[daemon]
image = "test-image:latest"
`), 0644)

	// Remove docker from PATH.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", t.TempDir())

	dr := &doctorRunner{projectDir: dir, timeout: 5 * time.Second}
	result := dr.checkAgentRoundtrip()
	if result.status != checkFail {
		t.Errorf("expected checkFail when docker not found, got %v", result.status)
	}
}

// TestCheckAgentRoundtrip_timeout verifies timeout behavior.
func TestCheckAgentRoundtrip_timeout(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
[daemon]
image = "nonexistent-image-that-will-fail:latest"
`), 0644)

	// Use a very short timeout; the docker run will fail quickly since the image
	// doesn't exist, which tests that the timeout path and error path both work.
	dr := &doctorRunner{projectDir: dir, timeout: 5 * time.Second}
	result := dr.checkAgentRoundtrip()
	// Either timeout or failure is acceptable here (image doesn't exist).
	if result.status != checkFail {
		t.Errorf("expected checkFail for nonexistent image, got %v", result.status)
	}
}

// TestScanForTODOMarkers verifies the marker-counting helper.
func TestScanForTODOMarkers(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(filepath.Join(clocheDir, "prompts"), 0755)

	// No markers.
	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte("workflow clean {}\n"), 0644)
	if n := scanForTODOMarkers(clocheDir); n != 0 {
		t.Errorf("expected 0 markers, got %d", n)
	}

	// One marker in a workflow file.
	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte("# TODO(cloche-init): fix me\n"), 0644)
	if n := scanForTODOMarkers(clocheDir); n != 1 {
		t.Errorf("expected 1 marker, got %d", n)
	}

	// Marker in Dockerfile.
	os.WriteFile(filepath.Join(clocheDir, "Dockerfile"), []byte("# TODO(cloche-init): add deps\n"), 0644)
	if n := scanForTODOMarkers(clocheDir); n != 2 {
		t.Errorf("expected 2 markers, got %d", n)
	}
}
