package egress

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRules(t *testing.T) {
	rs, err := ParseRules("api.anthropic.com, host.docker.internal:7777,*.example.com:8443")
	require.NoError(t, err)
	assert.Equal(t, []Rule{
		{"api.anthropic.com", "443"},
		{"host.docker.internal", "7777"},
		{"*.example.com", "8443"},
	}, rs)
	_, err = ParseRules("*")
	assert.Error(t, err)
}

func TestAllowed(t *testing.T) {
	rs, _ := ParseRules("api.anthropic.com,*.example.com:8443")
	assert.True(t, Allowed(rs, "api.anthropic.com:443"))
	assert.False(t, Allowed(rs, "api.anthropic.com:80"))
	assert.False(t, Allowed(rs, "github.com:443"))
	assert.True(t, Allowed(rs, "a.example.com:8443"))
	assert.False(t, Allowed(rs, "example.com.evil.net:8443"))
}

func startProxy(t *testing.T, p *Proxy) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.ServeListener(ctx, ln)
	return ln.Addr().String()
}

func connect(t *testing.T, proxy, target string) *http.Response {
	c, err := net.Dial("tcp", proxy)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Write([]byte("CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n"))
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "CONNECT"})
	require.NoError(t, err)
	return resp
}

func TestProxy_DeniesHostOutsideAllowlist(t *testing.T) {
	dialed := false
	p := &Proxy{
		Rules: []Rule{{"api.anthropic.com", "443"}},
		Dial: func(ctx context.Context, n, a string) (net.Conn, error) {
			dialed = true
			return nil, assert.AnError
		},
	}
	addr := startProxy(t, p)
	resp := connect(t, addr, "evil.example.org:443")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, dialed, "denied target must never be dialed")
}

func TestProxy_AllowsListedHost(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err == nil {
			c.Write([]byte("hi"))
			c.Close()
		}
	}()
	p := &Proxy{
		Rules: []Rule{{"model.test", "443"}},
		Dial: func(ctx context.Context, n, a string) (net.Conn, error) {
			return net.Dial("tcp", echo.Addr().String())
		},
	}
	addr := startProxy(t, p)
	resp := connect(t, addr, "model.test:443")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestProxy_RejectsPlainHTTP(t *testing.T) {
	addr := startProxy(t, &Proxy{Rules: []Rule{{"a", "80"}}})
	resp, err := http.Get("http://" + addr + "/")
	require.NoError(t, err)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestProxy_Forward(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer target.Close()
	go func() {
		c, err := target.Accept()
		if err == nil {
			c.Write([]byte("pong"))
			c.Close()
		}
	}()
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	fa := free.Addr().String()
	free.Close()
	p := &Proxy{Forwards: []Forward{{Listen: fa, Target: target.Addr().String()}}}
	startProxy(t, p)
	var c net.Conn
	for i := 0; i < 50; i++ {
		if c, err = net.Dial("tcp", fa); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	defer c.Close()
	buf := make([]byte, 4)
	c.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = c.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(buf))
}
