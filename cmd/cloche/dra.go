package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/swordsmanluke/cloche/internal/dra"
)

const draUsage = "usage: cloche dra list [--project <dir>]"

// cmdDRA dispatches "cloche dra <subcommand>".
func cmdDRA(args []string) {
	if err := draCommand(args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func draCommand(args []string, w io.Writer) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("%s", draUsage)
	}
	var project string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--project", "-p":
			if i+1 >= len(rest) {
				return fmt.Errorf("--project needs a directory")
			}
			i++
			project = rest[i]
		default:
			return fmt.Errorf("unknown argument %q\n%s", rest[i], draUsage)
		}
	}
	ds, err := dra.List(resolveProjectDir(project))
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		fmt.Fprintln(w, "No DRAs defined.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSESSION\tCHARTER")
	for _, d := range ds {
		heading := d.Heading
		if heading == "" {
			heading = "-"
		}
		// Session state is filled in by a later ticket.
		fmt.Fprintf(tw, "%s\t-\t%s\n", d.Name, heading)
	}
	return tw.Flush()
}
