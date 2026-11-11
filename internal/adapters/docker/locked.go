package docker

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/swordsmanluke/cloche/internal/adapters/egress"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// Locked-down containers (DRAs, CLOCHE-A118). Three limits on top of a normal
// run, none needing extra capabilities (req-b8f2):
//
//   - the project is read-only: everything under /workspace is root-owned and
//     not writable by the agent user, except the declared WritablePaths;
//   - the network is an allowlist: the container joins a Docker --internal
//     network whose only member besides itself is an egress sidecar
//     (`cloche-agent egress`), which relays the allowed destinations;
//   - no-new-privileges.

const (
	egressProxyPort = "3128"
	labelEgressNet  = "cloche.egress-network"
)

// DRAProfile builds the locked profile for a DRA: writable notes.md, network
// to the model API, the daemon, and Saga. Saga's address comes from
// CLOCHE_SAGA_ADDR (host:port on the Docker host); unset means no Saga access.
func DRAProfile(notesPath string) *ports.LockedConfig {
	lc := &ports.LockedConfig{
		WritablePaths: []string{notesPath},
		AllowHosts:    []string{"api.anthropic.com"},
	}
	daemon := os.Getenv("CLOCHE_ADDR")
	if daemon == "" {
		daemon = config.DefaultAddr()
	}
	// Each forward becomes a sidecar listener on the same port, so skip
	// duplicates and the proxy's own CONNECT listen port.
	add := func(addr string) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil || port == egressProxyPort {
			return
		}
		fwd := "host.docker.internal:" + port
		for _, f := range lc.ForwardAddrs {
			if f == fwd {
				return
			}
		}
		lc.ForwardAddrs = append(lc.ForwardAddrs, fwd)
	}
	add(daemon)
	if saga := os.Getenv("CLOCHE_SAGA_ADDR"); saga != "" {
		add(saga)
	}
	return lc
}

// lockedWrapperCmd is the root-side setup + privilege drop for a locked
// container. Project files become root-owned and non-writable; the agent user
// owns only the writable paths and the run's own state dirs.
func lockedWrapperCmd(containerCmd []string, writable []string) string {
	var b strings.Builder
	b.WriteString("find /workspace -path /workspace/.cloche/runs -prune -o -exec chown root:root {} +")
	b.WriteString(" && find /workspace -path /workspace/.cloche/runs -prune -o -exec chmod go-w,u-w {} +")
	b.WriteString(" && mkdir -p /workspace/.cloche/output /workspace/.cloche/runs")
	b.WriteString(" && chown -R agent:agent /workspace/.cloche/output /workspace/.cloche/runs")
	for _, w := range writable {
		p := path.Join("/workspace", w)
		fmt.Fprintf(&b, " && mkdir -p '%s' && touch '%s' && chown agent:agent '%s' && chmod u+w '%s'", path.Dir(p), p, p, p)
		// The parent directory stays root-owned: the file can be edited in
		// place but not replaced, and siblings stay untouchable.
	}
	// The .claude dir may not exist; that is the only step allowed to fail.
	b.WriteString(" && { chown -R agent:agent /home/agent/.claude 2>/dev/null || true; }")
	// Fail closed: the agent is only exec'd when every lock step succeeded.
	// Otherwise exit non-zero with a message (surfaced via the container logs
	// in the run's error) instead of starting an unlocked agent.
	b.WriteString(" && exec gosu agent " + strings.Join(containerCmd, " "))
	b.WriteString(" || { echo 'cloche: locked container lock-down could not be applied; refusing to start the agent' >&2; exit 1; }")
	return b.String()
}

func egressNames(containerName string) (network, sidecar string) {
	return "cloche-dra-" + containerName, "cloche-dra-" + containerName + "-egress"
}

// egressCmd builds the sidecar's argv.
func egressCmd(lc *ports.LockedConfig) ([]string, error) {
	rules, err := egress.ParseRules(strings.Join(lc.AllowHosts, ","))
	if err != nil {
		return nil, err
	}
	argv := []string{"cloche-agent", "egress", "--listen", ":" + egressProxyPort}
	for _, r := range rules {
		argv = append(argv, "--allow", r.Host+":"+r.Port)
	}
	for _, f := range lc.ForwardAddrs {
		_, port, err := net.SplitHostPort(f)
		if err != nil {
			return nil, fmt.Errorf("invalid forward %q: %w", f, err)
		}
		argv = append(argv, "--forward", ":"+port+"="+f)
	}
	return argv, nil
}

// lockedNetworkArgs are the docker create args that replace the default
// networking for a locked container, given the sidecar's address on the
// internal network.
func lockedNetworkArgs(network, sidecarIP string) []string {
	proxy := "http://" + sidecarIP + ":" + egressProxyPort
	return []string{
		"--network", network,
		"--add-host", "host.docker.internal:" + sidecarIP,
		"--security-opt", "no-new-privileges",
		"--label", labelEgressNet + "=" + network,
		"-e", "HTTPS_PROXY=" + proxy,
		"-e", "https_proxy=" + proxy,
		"-e", "NO_PROXY=host.docker.internal,localhost,127.0.0.1",
		"-e", "no_proxy=host.docker.internal,localhost,127.0.0.1",
	}
}

func dockerRun(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %s: %w", strings.Join(args[:1], " "), strings.TrimSpace(errb.String()), err)
	}
	return strings.TrimSpace(out.String()), nil
}

// startEgress creates the internal network and the sidecar and returns the
// network name and the sidecar's IP on it. On error nothing is left behind.
func startEgress(ctx context.Context, containerName, image string, lc *ports.LockedConfig) (network, ip string, err error) {
	argv, err := egressCmd(lc)
	if err != nil {
		return "", "", err
	}
	network, sidecar := egressNames(containerName)
	if _, err = dockerRun(ctx, "network", "create", "--internal", network); err != nil {
		return "", "", err
	}
	cleanup := func() { teardownEgress(context.Background(), network) }
	args := append([]string{"create", "--name", sidecar,
		"--add-host=host.docker.internal:host-gateway",
		"--security-opt", "no-new-privileges", "--read-only",
		"--user", "agent", image}, argv...)
	if _, err = dockerRun(ctx, args...); err != nil {
		cleanup()
		return "", "", err
	}
	if _, err = dockerRun(ctx, "network", "connect", network, sidecar); err != nil {
		cleanup()
		return "", "", err
	}
	if _, err = dockerRun(ctx, "start", sidecar); err != nil {
		cleanup()
		return "", "", err
	}
	ip, err = dockerRun(ctx, "inspect", "-f",
		fmt.Sprintf(`{{(index .NetworkSettings.Networks %q).IPAddress}}`, network), sidecar)
	if err != nil || ip == "" {
		cleanup()
		return "", "", fmt.Errorf("egress sidecar has no address on %s: %v", network, err)
	}
	log.Printf("runtime.Start: egress sidecar %s on %s at %s", sidecar, network, ip)
	return network, ip, nil
}

// teardownEgress removes the sidecar and internal network for a network name.
// Safe to call more than once.
func teardownEgress(ctx context.Context, network string) {
	removeSidecar(ctx, network)
	exec.CommandContext(ctx, "docker", "network", "rm", network).Run()
}

// removeSidecar force-removes only the egress sidecar of a network. The
// network stays: it cannot be removed while a (kept) main container is
// attached, so Remove deletes it later.
func removeSidecar(ctx context.Context, network string) {
	exec.CommandContext(ctx, "docker", "rm", "-f", network+"-egress").Run()
}

// teardownSidecarFor removes the egress sidecar of a stopped locked container,
// if it had one. The container is kept, but a stopped container needs no proxy.
func teardownSidecarFor(ctx context.Context, containerID string) {
	if network := egressNetworkOf(ctx, containerID); network != "" {
		removeSidecar(ctx, network)
	}
}

func egressNetworkOf(ctx context.Context, containerID string) string {
	out, err := dockerRun(ctx, "inspect", "-f",
		fmt.Sprintf(`{{index .Config.Labels %q}}`, labelEgressNet), containerID)
	if err != nil || out == "<no value>" {
		return ""
	}
	return out
}

// teardownEgressFor removes the egress resources of a container, if it had any.
func teardownEgressFor(ctx context.Context, containerID string) {
	if network := egressNetworkOf(ctx, containerID); network != "" {
		teardownEgress(ctx, network)
	}
}

// DRAImage is the image `make install` builds from docker/cloche-dra.
const DRAImage = "cloche-dra:latest"

// ApplyProfile applies a workflow's `container { profile = ... }` selection to
// a container config. Profile "dra" selects the DRA image (unless the
// workflow names an image) and the locked-down limits; the only writable
// project path is `container.notes` (default .cloche/dras/<workflow>/notes.md).
// An empty profile leaves cfg alone; an unknown one is an error rather than a
// silent fall-back to an unrestricted container.
func ApplyProfile(cfg *ports.ContainerConfig, workflow string, wfConfig map[string]string) error {
	switch profile := wfConfig["container.profile"]; profile {
	case "":
		return nil
	case "dra":
		notes := wfConfig["container.notes"]
		if notes == "" {
			notes = path.Join(".cloche", "dras", workflow, "notes.md")
		}
		clean := path.Clean(notes)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
			return fmt.Errorf("container.notes %q must be a file path inside the project", notes)
		}
		if strings.ContainsAny(clean, "'\n") {
			return fmt.Errorf("container.notes %q contains an unsupported character", notes)
		}
		if wfConfig["container.image"] == "" {
			cfg.Image = DRAImage
		}
		cfg.Locked = DRAProfile(clean)
		return nil
	default:
		return fmt.Errorf("unknown container profile %q (known: dra)", profile)
	}
}

// copyWritableOut copies each of the session's writable paths from the
// (stopped) container back to the same project-relative path on the host.
// A path the agent never wrote comes back as the empty file the wrapper
// created, which does not clobber existing notes: empty results are skipped.
func copyWritableOut(ctx context.Context, rt ports.ContainerRuntime, sess *ContainerSession) error {
	if sess.locked == nil || sess.hostProjectDir == "" {
		return nil
	}
	var firstErr error
	for _, w := range sess.locked.WritablePaths {
		dst := filepath.Join(sess.hostProjectDir, filepath.FromSlash(w))
		tmp, err := os.CreateTemp("", "cloche-writable-*")
		if err != nil {
			return err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := rt.CopyFrom(ctx, sess.ContainerID, "/workspace/"+w, tmp.Name()); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("copying %s out: %w", w, err)
			}
			continue
		}
		data, err := os.ReadFile(tmp.Name())
		if err != nil || len(data) == 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
