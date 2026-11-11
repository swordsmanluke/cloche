package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/dra"
)

const draUsage = "usage: cloche dra list [--project <dir>]\n       cloche dra wake <name> --message <text> [--project <dir>]"

// wakeDRAFn starts a wake-up through the daemon and returns the run id and
// any budget warning. A variable so tests need no daemon.
var wakeDRAFn = wakeDRAViaDaemon

func wakeDRAViaDaemon(project, name, message string) (runID, warning string, err error) {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", "", fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := pb.NewClocheServiceClient(conn).RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName: dra.WakeRequest,
		ProjectDir:   project,
		Title:        "DRA wake-up: " + name,
		Env:          map[string]string{dra.WakeEnvDRA: name, dra.WakeEnvMessage: message},
	})
	if err != nil {
		return "", "", err
	}
	return resp.RunId, resp.BudgetWarning, nil
}

// cmdDRA dispatches "cloche dra <subcommand>".
func cmdDRA(args []string) {
	if err := draCommand(args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func draCommand(args []string, w io.Writer) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "wake") {
		return fmt.Errorf("%s", draUsage)
	}
	wake := args[0] == "wake"
	var project, message, name string
	var haveMessage bool
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--project", "-p":
			if i+1 >= len(rest) {
				return fmt.Errorf("--project needs a directory")
			}
			i++
			project = rest[i]
		case "--message", "-m":
			if !wake || i+1 >= len(rest) {
				return fmt.Errorf("--message needs text (and only applies to wake)")
			}
			i++
			message, haveMessage = rest[i], true
		default:
			if wake && name == "" && !strings.HasPrefix(rest[i], "-") {
				name = rest[i]
				continue
			}
			return fmt.Errorf("unknown argument %q\n%s", rest[i], draUsage)
		}
	}
	if wake {
		if name == "" || !haveMessage || strings.TrimSpace(message) == "" {
			return fmt.Errorf("%s", draUsage)
		}
		runID, warning, err := wakeDRAFn(resolveProjectDir(project), name, message)
		if err != nil {
			return err
		}
		if warning != "" {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		fmt.Fprintf(w, "Started wake-up: %s\n", runID)
		return nil
	}
	ds, err := dra.List(resolveProjectDir(project))
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		fmt.Fprintln(w, "No DRAs defined.")
		return nil
	}
	sessions, err := dra.LoadSessions(resolveProjectDir(project))
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSESSION\tLAST WAKE-UP\tCHARTER")
	for _, d := range ds {
		heading := d.Heading
		if heading == "" {
			heading = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Name, sessionCell(sessions[d.Name]), lastWakeCell(sessions[d.Name]), heading)
	}
	return tw.Flush()
}

func sessionCell(s dra.Session) string {
	if s.ID == "" {
		return "-"
	}
	return s.ID
}

// lastWakeCell is "<run id> <result> <time>", or "-" before the first wake-up.
func lastWakeCell(s dra.Session) string {
	if s.LastRunID == "" {
		return "-"
	}
	out := s.LastRunID
	if s.LastResult != "" {
		out += " " + s.LastResult
	}
	if s.LastMode != "" {
		out += " (" + s.LastMode + ")"
	}
	if !s.LastWakeAt.IsZero() {
		out += " " + s.LastWakeAt.Local().Format("2006-01-02 15:04")
	}
	return out
}
