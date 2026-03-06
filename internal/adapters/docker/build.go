package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const dockerfileHashLabel = "cloche.dockerfile.hash"

// EnsureImage checks whether the Docker image is up-to-date with the project
// Dockerfile. If the image does not exist or the Dockerfile has changed since
// the image was built, it rebuilds automatically.
func (r *Runtime) EnsureImage(ctx context.Context, projectDir, image string) error {
	dockerfilePath := filepath.Join(projectDir, ".cloche", "Dockerfile")

	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// No project Dockerfile — nothing to validate.
			return nil
		}
		return fmt.Errorf("reading Dockerfile: %w", err)
	}

	currentHash := hashBytes(content)

	storedHash, err := imageLabel(ctx, image, dockerfileHashLabel)
	if err == nil && storedHash == currentHash {
		log.Printf("image %s is up-to-date (dockerfile hash %s)", image, currentHash[:12])
		return nil
	}

	// Image is missing or stale — rebuild.
	if err != nil {
		log.Printf("image %s not found or cannot inspect; building", image)
	} else {
		log.Printf("image %s is stale (have %s, want %s); rebuilding", image, storedHash[:12], currentHash[:12])
	}

	return buildImage(ctx, projectDir, dockerfilePath, image, currentHash)
}

// hashBytes returns the hex-encoded SHA-256 digest of data.
func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// imageLabel retrieves a label value from a Docker image.
// Returns an error if the image does not exist or cannot be inspected.
func imageLabel(ctx context.Context, image, label string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", fmt.Sprintf("{{index .Config.Labels %q}}", label),
		image)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("inspecting image %s: %s: %w", image, strings.TrimSpace(stderr.String()), err)
	}
	val := strings.TrimSpace(stdout.String())
	if val == "" || val == "<no value>" {
		return "", fmt.Errorf("label %s not set on image %s", label, image)
	}
	return val, nil
}

// buildImage runs docker build for the project, tagging the result with the
// given image name and embedding the Dockerfile hash as a label.
func buildImage(ctx context.Context, projectDir, dockerfilePath, image, hash string) error {
	log.Printf("building image %s from %s", image, dockerfilePath)

	args := []string{
		"build",
		"-t", image,
		"-f", dockerfilePath,
		"--label", fmt.Sprintf("%s=%s", dockerfileHashLabel, hash),
		projectDir,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stderr // stream build output to daemon stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building image %s: %w", image, err)
	}

	log.Printf("image %s built successfully", image)
	return nil
}
