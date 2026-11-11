package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/dashauth"
	"github.com/swordsmanluke/cloche/internal/signal"
)

func newAuthHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	da := dashauth.NewStore(filepath.Join(t.TempDir(), "dashboard-token"))
	tok, err := da.Get()
	require.NoError(t, err)
	h, err := NewHandler(store, store,
		WithDashboardAuth(da),
		WithHelpMCP([]byte("secret"), func(context.Context, string, string, string, string, []string, string) (string, string, bool, error) {
			return "", "", false, nil
		}),
		WithSignalIntake(signal.NewTokenStore(t.TempDir()), &signal.Dispatcher{Store: store}))
	require.NoError(t, err)
	return h, tok
}

// concretePath fills a mux pattern's wildcards with placeholder values.
func concretePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			b.WriteByte(p[i])
			continue
		}
		j := strings.IndexByte(p[i:], '}')
		if p[i:i+j+1] != "{$}" {
			b.WriteString("x")
		}
		i += j
	}
	return b.String()
}

func do(h http.Handler, method, path, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const (
	loopbackAddr = "127.0.0.1:5555"
	remoteAddr   = "192.168.1.9:5555"
)

// Every registered mutating route refuses a request without the token, from
// loopback too. Walking h.patterns means a new route can't be missed.
func TestMutatingRoutesRefuseWithoutToken(t *testing.T) {
	h, tok := newAuthHandler(t)
	n := 0
	for _, pat := range h.patterns {
		method, p, _ := strings.Cut(pat, " ")
		if !isMutating(method) {
			continue
		}
		n++
		path := concretePath(p)
		for _, addr := range []string{loopbackAddr, remoteAddr} {
			rec := do(h, method, path, addr, nil)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s from %s without token", pat, addr)
			rec = do(h, method, path, addr, map[string]string{"Authorization": "Bearer wrong"})
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s from %s with wrong token", pat, addr)
		}
		if !tokenExemptPatterns[pat] {
			rec := do(h, method, path, loopbackAddr, map[string]string{"Authorization": "Bearer " + tok})
			assert.NotEqual(t, http.StatusUnauthorized, rec.Code, "%s with token", pat)
			assert.NotEqual(t, http.StatusForbidden, rec.Code, "%s with token", pat)
			// cross-site browser post is refused even with the cookie
			rec = do(h, method, path, loopbackAddr, map[string]string{
				"Cookie": dashboardCookie + "=" + tok, "Origin": "http://evil.example"})
			assert.Equal(t, http.StatusForbidden, rec.Code, "%s cross-origin", pat)
		}
	}
	assert.Greater(t, n, 15, "expected to find the mutating routes")
}

// Routes that carry their own credential must not be gated on the dashboard
// token: they keep answering from their own check.
func TestExemptRoutesDoNotNeedDashboardToken(t *testing.T) {
	h, _ := newAuthHandler(t)
	// /mcp validates its own per-run token; a request without it gets the
	// handler's 401 (WWW-Authenticate from the MCP layer), not a redirect/403.
	rec := do(h, "POST", "/mcp", "172.17.0.2:4000", nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), "dashboard token")
}

func TestReadsNeedTokenOnlyFromNonLoopback(t *testing.T) {
	h, tok := newAuthHandler(t)
	const path = "/api/projects"

	assert.Equal(t, http.StatusOK, do(h, "GET", path, loopbackAddr, nil).Code, "loopback read without token")
	assert.Equal(t, http.StatusOK, do(h, "GET", path, "[::1]:5555", nil).Code, "ipv6 loopback")

	assert.Equal(t, http.StatusUnauthorized, do(h, "GET", path, remoteAddr, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, do(h, "GET", path, remoteAddr, map[string]string{"Authorization": "Bearer nope"}).Code)
	assert.Equal(t, http.StatusOK, do(h, "GET", path, remoteAddr, map[string]string{"Authorization": "Bearer " + tok}).Code)
	assert.Equal(t, http.StatusOK, do(h, "GET", path, remoteAddr, map[string]string{"Cookie": dashboardCookie + "=" + tok}).Code)

	// A forwarded header never makes a remote client local.
	rec := do(h, "GET", path, remoteAddr, map[string]string{"X-Forwarded-For": "127.0.0.1", "Forwarded": "for=127.0.0.1"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Every read-only route is guarded for remote clients (except login).
	for _, pat := range h.patterns {
		method, p, _ := strings.Cut(pat, " ")
		if isMutating(method) || tokenExemptPatterns[pat] {
			continue
		}
		rec := do(h, method, concretePath(p), remoteAddr, nil)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s from remote without token", pat)
	}
}

func TestBrowserNavigationRedirectsToLogin(t *testing.T) {
	h, _ := newAuthHandler(t)
	rec := do(h, "GET", "/", remoteAddr, map[string]string{"Accept": "text/html"})
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestLoginSetsStrictHttpOnlyCookie(t *testing.T) {
	h, tok := newAuthHandler(t)

	req := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {"bad"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Result().Cookies())

	req = httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {tok}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
	cs := rec.Result().Cookies()
	require.Len(t, cs, 1)
	assert.True(t, cs[0].HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, cs[0].SameSite)
	assert.Equal(t, tok, cs[0].Value)

	// Cross-origin login attempts are refused.
	req = httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {tok}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	req.RemoteAddr = remoteAddr
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// The login page itself is reachable without a token.
	assert.Equal(t, http.StatusOK, do(h, "GET", "/login", remoteAddr, nil).Code)
}

func TestSameOriginMutationWithCookie(t *testing.T) {
	h, tok := newAuthHandler(t)
	rec := do(h, "POST", "/api/runs/x/stop", loopbackAddr, map[string]string{
		"Cookie": dashboardCookie + "=" + tok, "Origin": "http://example.com"})
	assert.NotEqual(t, http.StatusForbidden, rec.Code)
	assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
}
