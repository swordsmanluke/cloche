package docker

import "testing"

// Daemon and Saga on the same port (or Saga on the proxy port) must not yield
// duplicate/conflicting sidecar listeners.
func TestDRAProfile_NoDuplicateForwardPorts(t *testing.T) {
	t.Setenv("CLOCHE_ADDR", "127.0.0.1:50051")
	t.Setenv("CLOCHE_SAGA_ADDR", "127.0.0.1:50051")
	lc := DRAProfile(".cloche/dras/x/notes.md")
	seen := map[string]bool{}
	for _, f := range lc.ForwardAddrs {
		if seen[f] {
			t.Fatalf("duplicate forward %q: sidecar would fail with address already in use", f)
		}
		seen[f] = true
	}
	t.Setenv("CLOCHE_SAGA_ADDR", "127.0.0.1:3128")
	lc = DRAProfile("n.md")
	argv, _ := egressCmd(lc)
	for _, f := range lc.ForwardAddrs {
		if f == "host.docker.internal:"+egressProxyPort {
			t.Fatalf("forward on proxy port conflicts with CONNECT listener: %v", argv)
		}
	}
}
