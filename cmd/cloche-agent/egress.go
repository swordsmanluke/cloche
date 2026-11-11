package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"strings"
	"syscall"

	"github.com/swordsmanluke/cloche/internal/adapters/egress"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// runEgress runs the allowlisting egress proxy that fronts a locked-down
// (DRA) container's network. See internal/adapters/egress.
//
//	cloche-agent egress --listen :3128 --allow api.anthropic.com:443 \
//	    --forward :50051=host.docker.internal:50051
func runEgress(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("egress", flag.ContinueOnError)
	listen := fs.String("listen", ":3128", "CONNECT proxy listen address")
	var allow, fwd multiFlag
	fs.Var(&allow, "allow", "allowed host[:port] for CONNECT (repeatable)")
	fs.Var(&fwd, "forward", "TCP forward listen=target (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rules, err := egress.ParseRules(strings.Join(allow, ","))
	if err != nil {
		return err
	}
	p := &egress.Proxy{Rules: rules}
	for _, f := range fwd {
		l, t, ok := strings.Cut(f, "=")
		if !ok || l == "" || t == "" {
			return fmt.Errorf("invalid --forward %q: want listen=target", f)
		}
		p.Forwards = append(p.Forwards, egress.Forward{Listen: l, Target: t})
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return p.Serve(ctx, *listen)
}
