// Package egress is the allowlisting network proxy that locked-down (DRA)
// containers reach the outside world through. The container sits on a Docker
// internal network with no route out; the only thing it can talk to is a
// sidecar running this proxy, which enforces the allowlist. That keeps the
// container unprivileged (no NET_ADMIN, no iptables inside it).
//
// Two mechanisms:
//   - an HTTP CONNECT proxy (HTTPS_PROXY) for the model API and any other
//     allowlisted host:port;
//   - fixed TCP forwards for endpoints that don't speak through a proxy
//     (the daemon's gRPC port on the host).
package egress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rule is one allowlist entry: a host (exact, or "*.example.com" for any
// subdomain) and a port.
type Rule struct {
	Host string
	Port string
}

// ParseRule parses "host:port" or "host" (port defaults to 443).
func ParseRule(s string) (Rule, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "*" || strings.Contains(s, "://") {
		return Rule{}, fmt.Errorf("invalid egress rule %q: want host or host:port (no wildcards-for-everything)", s)
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = s, "443"
	}
	if host == "" || host == "*" {
		return Rule{}, fmt.Errorf("invalid egress rule %q", s)
	}
	return Rule{Host: host, Port: port}, nil
}

// ParseRules parses a comma-separated list.
func ParseRules(list string) ([]Rule, error) {
	var out []Rule
	for _, p := range strings.Split(list, ",") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		r, err := ParseRule(p)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Allowed reports whether host:port matches any rule.
func Allowed(rules []Rule, hostport string) bool {
	host, port, err := net.SplitHostPort(strings.ToLower(hostport))
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.Port != port {
			continue
		}
		if r.Host == host {
			return true
		}
		if strings.HasPrefix(r.Host, "*.") && strings.HasSuffix(host, r.Host[1:]) {
			return true
		}
	}
	return false
}

// Forward relays a local listen address to a fixed target.
type Forward struct {
	Listen string // e.g. ":50051"
	Target string // e.g. "host.docker.internal:50051"
}

// Proxy is the allowlisting CONNECT proxy plus TCP forwards.
type Proxy struct {
	Rules    []Rule
	Forwards []Forward
	// Dial overrides net.Dialer (tests).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	mu        sync.Mutex
	listeners []net.Listener
}

func (p *Proxy) dial(ctx context.Context, addr string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(ctx, "tcp", addr)
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, "tcp", addr)
}

// Handler returns the CONNECT handler.
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "only CONNECT is supported", http.StatusMethodNotAllowed)
			return
		}
		if !Allowed(p.Rules, r.Host) {
			http.Error(w, "egress denied: "+r.Host+" is not on the allowlist", http.StatusForbidden)
			return
		}
		up, err := p.dial(r.Context(), r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			up.Close()
			http.Error(w, "cannot hijack", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		c, _, err := hj.Hijack()
		if err != nil {
			up.Close()
			return
		}
		pipe(c, up)
	})
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// Serve listens for CONNECT requests on proxyAddr and starts every forward.
// It returns when ctx is done or the proxy listener fails.
func (p *Proxy) Serve(ctx context.Context, proxyAddr string) error {
	ln, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		return err
	}
	return p.ServeListener(ctx, ln)
}

// ServeListener is Serve on an existing listener.
func (p *Proxy) ServeListener(ctx context.Context, ln net.Listener) error {
	p.track(ln)
	for _, f := range p.Forwards {
		fl, err := net.Listen("tcp", f.Listen)
		if err != nil {
			ln.Close()
			return fmt.Errorf("forward %s: %w", f.Listen, err)
		}
		p.track(fl)
		go p.serveForward(ctx, fl, f.Target)
	}
	srv := &http.Server{Handler: p.Handler()}
	go func() {
		<-ctx.Done()
		srv.Close()
		p.closeAll()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (p *Proxy) track(l net.Listener) {
	p.mu.Lock()
	p.listeners = append(p.listeners, l)
	p.mu.Unlock()
}

func (p *Proxy) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.listeners {
		l.Close()
	}
}

func (p *Proxy) serveForward(ctx context.Context, l net.Listener, target string) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			up, err := p.dial(ctx, target)
			if err != nil {
				c.Close()
				return
			}
			pipe(c, up)
		}()
	}
}
