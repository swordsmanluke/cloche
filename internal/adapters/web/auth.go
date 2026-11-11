package web

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// dashboardCookie carries the daemon-wide dashboard token for browsers. It is
// set once by the login page: HttpOnly so scripts on the page can't read it,
// SameSite=Strict so another site can't make the browser send it.
const dashboardCookie = "cloche_dashboard_token"

// tokenExemptPatterns are the routes the dashboard token does not guard
// because they carry their own credential, checked by the handler: the
// per-project signal token and the per-run MCP token. The login routes are
// exempt too, since they are how a browser gets the token in the first place.
var tokenExemptPatterns = map[string]bool{
	"POST /api/projects/{name}/signals":      true,
	"POST /api/projects/{name}/tasks/create": true,
	"POST /api/projects/{name}/tasks/update": true,
	"POST /mcp":                              true,
	"GET /login":                             true,
	"POST /login":                            true,
}

// ownCredentialPatterns skip the Origin check as well: they are authenticated
// by a bearer token a cross-site page cannot attach, never by the cookie.
func ownCredential(pattern string) bool {
	return tokenExemptPatterns[pattern] && !strings.HasSuffix(pattern, "/login")
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// isLoopback decides "this machine" from the connection's remote address
// only. Forwarded headers are never consulted: any client can set them.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// originOK refuses cross-site browser requests: an Origin header, when
// present, must name this host; "null" and unparsable values are refused.
func originOK(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
		return true
	}
	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

// presentedToken is the bearer token, or failing that the login cookie.
func presentedToken(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if c, err := r.Cookie(dashboardCookie); err == nil {
		return c.Value
	}
	return ""
}

// authMiddleware is the single dashboard access check, wrapped around the mux:
//   - mutating routes need the dashboard token from any address, loopback
//     included, and pass an Origin check;
//   - read-only routes need it only when the connection is not from loopback;
//   - routes with their own credential (signals, tasks create/update, /mcp)
//     and the login page are left to their handlers.
//
// Routing the request first means a new route is guarded by default: the only
// way out is an entry in tokenExemptPatterns.
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.dashAuth == nil {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := h.mux.Handler(r)
		if tokenExemptPatterns[pattern] {
			if !ownCredential(pattern) && r.Method == http.MethodPost && !originOK(r) {
				writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "cross-origin request refused"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		mutating := isMutating(r.Method)
		if mutating && !originOK(r) {
			writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "cross-origin request refused"})
			return
		}
		if mutating || !isLoopback(r) {
			if !h.dashAuth.Valid(presentedToken(r)) {
				h.denyDashboard(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) denyDashboard(w http.ResponseWriter, r *http.Request) {
	// A browser navigating to a page goes to the login form; everything else
	// (API calls, scripts) gets a plain 401.
	if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") &&
		strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="cloche dashboard"`)
	writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid dashboard token"})
}

const loginPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>cloche login</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body{font-family:sans-serif;max-width:26rem;margin:4rem auto;padding:0 1rem}
input{width:100%%;padding:.5rem;margin:.5rem 0;box-sizing:border-box}
.err{color:#b00020}</style></head>
<body><h1>cloche</h1>
<p>Enter the dashboard token (<code>cloche dashboard token</code>).</p>
%s<form method="post" action="/login">
<input type="password" name="token" autocomplete="current-password" autofocus required>
<button type="submit">Log in</button></form></body></html>`

func (h *Handler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.Replace(loginPage, "%s", "", 1)))
}

// handleLogin checks the submitted token and, if right, stores it in the
// session cookie. It always redirects to "/", never to a caller-chosen URL.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || h.dashAuth == nil || !h.dashAuth.Valid(strings.TrimSpace(r.PostForm.Get("token"))) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(strings.Replace(loginPage, "%s", `<p class="err">Invalid token.</p>`, 1)))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     dashboardCookie,
		Value:    strings.TrimSpace(r.PostForm.Get("token")),
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
