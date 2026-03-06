package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashBytes(t *testing.T) {
	h1 := hashBytes([]byte("hello"))
	h2 := hashBytes([]byte("hello"))
	h3 := hashBytes([]byte("world"))

	assert.Equal(t, h1, h2, "same input should produce same hash")
	assert.NotEqual(t, h1, h3, "different input should produce different hash")
	assert.Len(t, h1, 64, "SHA-256 hex digest should be 64 chars")
}

func TestEnsureImage_NoDockerfile(t *testing.T) {
	rt := &Runtime{}
	dir := t.TempDir()

	// No .cloche/Dockerfile — should succeed (nothing to validate)
	err := rt.EnsureImage(context.Background(), dir, "test-image:latest")
	assert.NoError(t, err)
}

func TestImageLabel_NonexistentImage(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available")
	}

	_, err := imageLabel(context.Background(), "cloche-nonexistent-image-xyz:latest", dockerfileHashLabel)
	assert.Error(t, err)
}

func TestEnsureImage_BuildsWhenImageMissing(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available")
	}

	dir := t.TempDir()
	dockerfileDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(dockerfileDir, 0755))

	// Write a minimal Dockerfile
	dockerfile := []byte("FROM alpine:latest\nRUN echo hello\n")
	require.NoError(t, os.WriteFile(filepath.Join(dockerfileDir, "Dockerfile"), dockerfile, 0644))

	image := "cloche-test-ensure-build:latest"
	// Clean up any pre-existing image
	exec.Command("docker", "rmi", "-f", image).Run()
	defer exec.Command("docker", "rmi", "-f", image).Run()

	rt := &Runtime{}
	err := rt.EnsureImage(context.Background(), dir, image)
	require.NoError(t, err)

	// Verify the image exists and has the label
	label, err := imageLabel(context.Background(), image, dockerfileHashLabel)
	require.NoError(t, err)
	assert.Equal(t, hashBytes(dockerfile), label)
}

func TestEnsureImage_SkipsWhenUpToDate(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available")
	}

	dir := t.TempDir()
	dockerfileDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(dockerfileDir, 0755))

	dockerfile := []byte("FROM alpine:latest\nRUN echo skip-test\n")
	require.NoError(t, os.WriteFile(filepath.Join(dockerfileDir, "Dockerfile"), dockerfile, 0644))

	image := "cloche-test-ensure-skip:latest"
	exec.Command("docker", "rmi", "-f", image).Run()
	defer exec.Command("docker", "rmi", "-f", image).Run()

	rt := &Runtime{}

	// First call: builds
	err := rt.EnsureImage(context.Background(), dir, image)
	require.NoError(t, err)

	// Second call with same Dockerfile: should skip (no rebuild)
	err = rt.EnsureImage(context.Background(), dir, image)
	require.NoError(t, err)

	// Image should still have the correct label
	label, err := imageLabel(context.Background(), image, dockerfileHashLabel)
	require.NoError(t, err)
	assert.Equal(t, hashBytes(dockerfile), label)
}

func TestParseBaseImage(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		want       string
	}{
		{
			name:       "simple FROM",
			dockerfile: "FROM ubuntu:22.04\nRUN echo hello\n",
			want:       "ubuntu:22.04",
		},
		{
			name:       "FROM with AS stage",
			dockerfile: "FROM golang:1.21 AS builder\nRUN go build\nFROM alpine:3.18\nCOPY --from=builder /app /app\n",
			want:       "alpine:3.18",
		},
		{
			name:       "multi-stage uses last FROM",
			dockerfile: "FROM node:18 AS frontend\nRUN npm build\nFROM golang:1.21 AS backend\nRUN go build\nFROM ubuntu:22.04\nCOPY --from=frontend /dist /dist\n",
			want:       "ubuntu:22.04",
		},
		{
			name:       "FROM scratch",
			dockerfile: "FROM scratch\nCOPY app /app\n",
			want:       "scratch",
		},
		{
			name:       "no FROM",
			dockerfile: "RUN echo hello\n",
			want:       "",
		},
		{
			name:       "comments and blank lines",
			dockerfile: "# This is a comment\n\nFROM alpine:latest\nRUN echo hello\n",
			want:       "alpine:latest",
		},
		{
			name:       "lowercase from",
			dockerfile: "from ubuntu:20.04\nRUN echo hello\n",
			want:       "ubuntu:20.04",
		},
		{
			name:       "FROM with platform",
			dockerfile: "FROM --platform=linux/amd64 golang:1.21\nRUN echo hello\n",
			want:       "golang:1.21",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBaseImage([]byte(tt.dockerfile))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsBaseImageStale_NoLabel(t *testing.T) {
	// When the built image doesn't have the base digest label, staleness
	// cannot be determined — should return false.
	// This is a unit-level test that doesn't need Docker since we test with
	// a nonexistent image that will fail the imageLabel call.
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available")
	}
	content := []byte("FROM alpine:latest\nRUN echo hello\n")
	stale, _ := isBaseImageStale(context.Background(), "cloche-nonexistent-image-xyz:latest", content)
	assert.False(t, stale)
}

func TestEnsureImage_StoresBaseImageDigest(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available")
	}

	dir := t.TempDir()
	dockerfileDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(dockerfileDir, 0755))

	dockerfile := []byte("FROM alpine:latest\nRUN echo base-digest-test\n")
	require.NoError(t, os.WriteFile(filepath.Join(dockerfileDir, "Dockerfile"), dockerfile, 0644))

	image := "cloche-test-base-digest:latest"
	exec.Command("docker", "rmi", "-f", image).Run()
	defer exec.Command("docker", "rmi", "-f", image).Run()

	rt := &Runtime{}
	err := rt.EnsureImage(context.Background(), dir, image)
	require.NoError(t, err)

	// Verify the base image digest label was stored
	label, err := imageLabel(context.Background(), image, baseImageDigestLabel)
	require.NoError(t, err)
	assert.NotEmpty(t, label, "base image digest label should be set")

	// The stored digest should match the current alpine:latest image ID
	alpineID, err := baseImageID(context.Background(), "alpine:latest")
	require.NoError(t, err)
	assert.Equal(t, alpineID, label)
}

func TestEnsureImage_RebuildsWhenDockerfileChanges(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available")
	}

	dir := t.TempDir()
	dockerfileDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(dockerfileDir, 0755))

	dockerfilePath := filepath.Join(dockerfileDir, "Dockerfile")

	// Initial Dockerfile
	v1 := []byte("FROM alpine:latest\nRUN echo v1\n")
	require.NoError(t, os.WriteFile(dockerfilePath, v1, 0644))

	image := "cloche-test-ensure-rebuild:latest"
	exec.Command("docker", "rmi", "-f", image).Run()
	defer exec.Command("docker", "rmi", "-f", image).Run()

	rt := &Runtime{}

	// Build with v1
	err := rt.EnsureImage(context.Background(), dir, image)
	require.NoError(t, err)

	label1, err := imageLabel(context.Background(), image, dockerfileHashLabel)
	require.NoError(t, err)
	assert.Equal(t, hashBytes(v1), label1)

	// Change Dockerfile to v2
	v2 := []byte("FROM alpine:latest\nRUN echo v2\n")
	require.NoError(t, os.WriteFile(dockerfilePath, v2, 0644))

	// Should detect change and rebuild
	err = rt.EnsureImage(context.Background(), dir, image)
	require.NoError(t, err)

	label2, err := imageLabel(context.Background(), image, dockerfileHashLabel)
	require.NoError(t, err)
	assert.Equal(t, hashBytes(v2), label2)
	assert.NotEqual(t, label1, label2, "label should change after rebuild")
}
