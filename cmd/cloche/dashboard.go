package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/dashauth"
)

const dashboardUsage = "usage: cloche dashboard token [--rotate]"

// cmdDashboard dispatches "cloche dashboard <subcommand>".
func cmdDashboard(args []string) {
	if err := dashboardCommand(args, dashauth.NewStore(config.DashboardTokenPath()), os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// dashboardCommand implements the dashboard subcommands against store.
func dashboardCommand(args []string, store *dashauth.Store, w io.Writer) error {
	if len(args) == 0 || args[0] != "token" {
		return fmt.Errorf("%s", dashboardUsage)
	}
	rotate := false
	for _, a := range args[1:] {
		if a != "--rotate" {
			return fmt.Errorf("unknown argument %q\n%s", a, dashboardUsage)
		}
		rotate = true
	}
	var (
		tok string
		err error
	)
	if rotate {
		tok, err = store.Rotate()
	} else {
		tok, err = store.Get()
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(w, tok)
	if rotate {
		fmt.Fprintln(os.Stderr, "token rotated: the previous token no longer works (browsers must log in again)")
	}
	return nil
}

// dashboardGet is http.Get for the daemon's dashboard listener, attaching the
// dashboard token when there is one to send: CLOCHE_DASHBOARD_TOKEN if set
// (for a daemon on another machine), otherwise the local token file when the
// target is this machine. Loopback reads don't need it, but a daemon bound to
// a LAN address sees this machine as a non-loopback client.
func dashboardGet(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil) //nolint:noctx
	if err != nil {
		return nil, err
	}
	if tok := dashboardClientToken(req.URL.Hostname()); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return http.DefaultClient.Do(req)
}

func dashboardClientToken(host string) string {
	if t := strings.TrimSpace(os.Getenv("CLOCHE_DASHBOARD_TOKEN")); t != "" {
		return t
	}
	if host == "" || host == "localhost" {
		return dashauth.NewStore(config.DashboardTokenPath()).Peek()
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return dashauth.NewStore(config.DashboardTokenPath()).Peek()
	}
	return ""
}
