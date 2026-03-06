package docker

import (
	"bufio"
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
const baseImageDigestLabel = "cloche.baseimage.digest"

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
	if err != nil {
		log.Printf("image %s not found or cannot inspect; building", image)
		return buildImage(ctx, projectDir, dockerfilePath, image, currentHash, content)
	}

	if storedHash != currentHash {
		log.Printf("image %s is stale (have %s, want %s); rebuilding", image, storedHash[:12], currentHash[:12])
		return buildImage(ctx, projectDir, dockerfilePath, image, currentHash, content)
	}

	// Dockerfile hash matches — check if the base image has changed locally.
	if baseStale, reason := isBaseImageStale(ctx, image, content); baseStale {
		log.Printf("image %s base image is stale (%s); rebuilding", image, reason)
		return buildImage(ctx, projectDir, dockerfilePath, image, currentHash, content)
	}

	log.Printf("image %s is up-to-date (dockerfile hash %s)", image, currentHash[:12])
	return nil
}

// parseBaseImage extracts the base image reference from the final FROM directive
// in a Dockerfile. It handles "FROM image:tag" and "FROM image:tag AS stage".
// Returns an empty string if no FROM directive is found.
func parseBaseImage(dockerfileContent []byte) string {
	var lastFrom string
	scanner := bufio.NewScanner(bytes.NewReader(dockerfileContent))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, "FROM ") {
			continue
		}
		// Extract the image reference. Skip any --flag=value options
		// (e.g. --platform=linux/amd64) that appear before the image.
		fields := strings.Fields(line)
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "--") {
				lastFrom = f
				break
			}
		}
	}
	return lastFrom
}

// baseImageID returns the image ID of a locally available Docker image.
// Returns an error if the image is not available locally.
func baseImageID(ctx context.Context, image string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}}", image)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("inspecting base image %s: %s: %w", image, strings.TrimSpace(stderr.String()), err)
	}
	id := strings.TrimSpace(stdout.String())
	if id == "" {
		return "", fmt.Errorf("empty image ID for %s", image)
	}
	return id, nil
}

// isBaseImageStale checks whether the base image used to build the project
// image has changed locally since build time. It compares the stored base
// image digest label to the current local image ID of the base image.
// Returns (true, reason) if stale, (false, "") if up-to-date or if the
// check cannot be performed (e.g. base image not available locally).
func isBaseImageStale(ctx context.Context, image string, dockerfileContent []byte) (bool, string) {
	baseRef := parseBaseImage(dockerfileContent)
	if baseRef == "" || strings.ToLower(baseRef) == "scratch" {
		return false, ""
	}

	storedDigest, err := imageLabel(ctx, image, baseImageDigestLabel)
	if err != nil {
		// Label not present (old build) — can't determine staleness.
		return false, ""
	}

	currentID, err := baseImageID(ctx, baseRef)
	if err != nil {
		// Base image not available locally — can't compare.
		return false, ""
	}

	if currentID != storedDigest {
		return true, fmt.Sprintf("base %s changed from %s to %s", baseRef, storedDigest[:12], currentID[:12])
	}
	return false, ""
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
// given image name and embedding the Dockerfile hash and base image digest
// as labels.
func buildImage(ctx context.Context, projectDir, dockerfilePath, image, hash string, dockerfileContent []byte) error {
	log.Printf("building image %s from %s", image, dockerfilePath)

	args := []string{
		"build",
		"-t", image,
		"-f", dockerfilePath,
		"--label", fmt.Sprintf("%s=%s", dockerfileHashLabel, hash),
	}

	// Record the base image digest so we can detect upstream changes later.
	if baseRef := parseBaseImage(dockerfileContent); baseRef != "" && strings.ToLower(baseRef) != "scratch" {
		if id, err := baseImageID(ctx, baseRef); err == nil {
			args = append(args, "--label", fmt.Sprintf("%s=%s", baseImageDigestLabel, id))
		}
	}

	args = append(args, projectDir)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stderr // stream build output to daemon stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building image %s: %w", image, err)
	}

	log.Printf("image %s built successfully", image)
	return nil
}
