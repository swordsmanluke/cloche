package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// overridesManifestDir and overridesManifestPath locate the record of what
// .cloche/overrides/ wrote into a container's /workspace. The manifest lives
// outside /workspace so it is never extracted, and is root-owned so the agent
// (which runs as `agent`) can read it but not rewrite it.
const (
	overridesManifestDir  = "/etc/cloche"
	overridesManifestPath = overridesManifestDir + "/overrides.json"
)

// overridesManifest records, for each /workspace-relative path an override
// wrote (slash-separated), the digest of the content written there. At
// extraction, a file whose content still matches is restored to its base
// commit state so overrides never reach a result branch; a file the agent
// edited afterwards is kept.
type overridesManifest struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

const overridesManifestVersion = 1

// buildOverridesManifest walks overridesDir and records the digest of every
// regular file in it, keyed by its path relative to overridesDir (which is
// also its path relative to /workspace, since overrides are copied onto the
// workspace root). A missing overridesDir yields an empty manifest.
// Non-regular entries (symlinks, devices) are skipped: their content in the
// container cannot be compared byte for byte, so extraction leaves them alone.
func buildOverridesManifest(overridesDir string) (overridesManifest, error) {
	m := overridesManifest{Version: overridesManifestVersion, Files: map[string]string{}}
	if _, err := os.Stat(overridesDir); errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	err := filepath.WalkDir(overridesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(overridesDir, p)
		if err != nil {
			return err
		}
		digest, err := fileDigest(p)
		if err != nil {
			return err
		}
		m.Files[filepath.ToSlash(rel)] = digest
		return nil
	})
	if err != nil {
		return overridesManifest{}, fmt.Errorf("recording overrides in %s: %w", overridesDir, err)
	}
	return m, nil
}

// fileDigest returns "sha256:<hex>" for the content of the file at p.
func fileDigest(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// writeOverridesManifest copies m into the (created, not yet started)
// container at overridesManifestPath. It streams a tar archive whose entries
// are owned by root: a plain `docker cp` of a host file keeps the host uid,
// which often equals the container's agent uid and would let the agent
// rewrite the manifest. The archive holds only cloche/ and its file, so
// extracting it into /etc leaves /etc's own ownership and mode alone.
func writeOverridesManifest(ctx context.Context, containerID string, m overridesManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	base := path.Base(overridesManifestDir)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: base + "/", Mode: 0755}); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: base + "/" + path.Base(overridesManifestPath), Mode: 0644, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", "cp", "-", containerID+":"+path.Dir(overridesManifestDir))
	cmd.Stdin = &buf
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker cp: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// readContainerFile returns the content of the regular file at p inside the
// container. A missing file yields an error wrapping fs.ErrNotExist. It is a
// package-private hook so tests can supply content without Docker.
var readContainerFile = func(ctx context.Context, containerID, p string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+p, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "Could not find the file") || strings.Contains(msg, "No such container:path") {
			return nil, fmt.Errorf("%s: %w", msg, fs.ErrNotExist)
		}
		return nil, fmt.Errorf("docker cp %s: %s: %w", p, msg, err)
	}
	tr := tar.NewReader(&stdout)
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("reading %s from container: %w", p, err)
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("%s in container is not a regular file", p)
	}
	return io.ReadAll(tr)
}

// readOverridesManifest fetches and parses the container's overrides
// manifest. found is false when the container has none (it was started by a
// cloche that predates the manifest). A manifest that exists but cannot be
// read or parsed is an error: skipping it would commit override content.
func readOverridesManifest(ctx context.Context, containerID string) (m overridesManifest, found bool, err error) {
	data, err := readContainerFile(ctx, containerID, overridesManifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return overridesManifest{}, false, nil
	}
	if err != nil {
		return overridesManifest{}, false, fmt.Errorf("reading overrides manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return overridesManifest{}, false, fmt.Errorf("parsing overrides manifest: %w", err)
	}
	if m.Version != overridesManifestVersion {
		return overridesManifest{}, false, fmt.Errorf("overrides manifest version %d not supported (want %d)", m.Version, overridesManifestVersion)
	}
	for p := range m.Files {
		if !validManifestPath(p) {
			return overridesManifest{}, false, fmt.Errorf("overrides manifest has invalid path %q", p)
		}
	}
	return m, true, nil
}

// validManifestPath accepts clean, relative, slash-separated paths that stay
// inside /workspace.
func validManifestPath(p string) bool {
	return p != "" && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

// untouchedOverrides sorts the manifest's paths that fall under worktreeDir's
// slice of /workspace (containerSubPath) into those still holding exactly
// what the override wrote. Paths are returned relative to the worktree. Files
// the agent edited, deleted, or replaced with a non-regular file are omitted:
// edits are kept as deliberate, and deletions go through the normal deletion
// handling.
func untouchedOverrides(m overridesManifest, worktreeDir, containerSubPath string) ([]string, error) {
	sub := strings.Trim(containerSubPath, "/")
	if sub == "." {
		sub = ""
	}
	var out []string
	for p, want := range m.Files {
		rel := p
		if sub != "" {
			if !strings.HasPrefix(p, sub+"/") {
				continue
			}
			rel = strings.TrimPrefix(p, sub+"/")
		}
		full := filepath.Join(worktreeDir, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		got, err := fileDigest(full)
		if err != nil {
			return nil, err
		}
		if got == want {
			out = append(out, rel)
		}
	}
	return out, nil
}
