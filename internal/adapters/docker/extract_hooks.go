package docker

import "context"

// SetDockerCpFunc replaces the dockerCp hook and returns a restore function.
// For use in tests outside the docker package that need to bypass the Docker daemon.
func SetDockerCpFunc(fn func(ctx context.Context, src, dst string) error) (restore func()) {
	old := dockerCp
	dockerCp = fn
	return func() { dockerCp = old }
}

// SetDockerExecFunc replaces the dockerExec hook and returns a restore function.
// For use in tests outside the docker package that need to bypass the Docker daemon.
func SetDockerExecFunc(fn func(ctx context.Context, containerID string, cmd ...string) ([]byte, error)) (restore func()) {
	old := dockerExec
	dockerExec = fn
	return func() { dockerExec = old }
}
