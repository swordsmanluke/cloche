package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/swordsmanluke/cloche/internal/module"
)

// cmdModule dispatches "cloche module <subcommand>". All subcommands are
// local file operations on .cloche/ — no daemon connection is needed, and
// nothing in a module is executed.
func cmdModule(args []string) {
	if err := moduleCommand(args, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func moduleCommand(args []string, in io.Reader, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cloche module add|list|update|remove (see 'cloche help module')")
	}
	switch args[0] {
	case "add":
		return moduleAddCommand(args[1:], w)
	case "list":
		return moduleListCommand(args[1:], w)
	case "update":
		return moduleUpdateCommand(args[1:], in, w)
	case "remove":
		return moduleRemoveCommand(args[1:], w)
	default:
		return fmt.Errorf("unknown module subcommand: %s", args[0])
	}
}

// moduleArgs splits args into positionals and the --project value.
func moduleArgs(args []string, extra map[string]*bool) (pos []string, project string, err error) {
	project = "."
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--project" || a == "-p" {
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("%s requires a value", a)
			}
			project = args[i+1]
			i++
			continue
		}
		if flag, ok := extra[a]; ok {
			*flag = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			return nil, "", fmt.Errorf("unknown flag: %s", a)
		}
		pos = append(pos, a)
	}
	return pos, project, nil
}

func moduleAddCommand(args []string, w io.Writer) error {
	pos, project, err := moduleArgs(args, nil)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: cloche module add <git-url>[@ref] | <path>")
	}
	spec, err := module.ParseSpec(pos[0])
	if err != nil {
		return err
	}
	res, err := module.Add(project, spec)
	if err != nil {
		return err
	}
	e := res.Entry
	if p, ok := e.LocalPath(); ok {
		fmt.Fprintf(w, "Added module %s (local path %s, used in place)\n", e.Name, p)
	} else {
		fmt.Fprintf(w, "Added module %s from %s at %s\n", e.Name, e.Source, shortCommit(e.Commit))
		fmt.Fprintf(w, "Vendored into .cloche/modules/%s/ — review and commit it along with .cloche/modules.lock.\n", e.Name)
	}
	if res.Prerequisites != "" {
		fmt.Fprintf(w, "\nPrerequisites (from the module README; cloche does not install these):\n\n%s\n", res.Prerequisites)
	}
	return nil
}

func moduleListCommand(args []string, w io.Writer) error {
	pos, project, err := moduleArgs(args, nil)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("usage: cloche module list")
	}
	entries, err := module.ReadLock(project)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "No modules installed.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE\tREF\tCOMMIT")
	for _, e := range entries {
		ref := e.Ref
		if _, local := e.LocalPath(); local {
			ref = "-"
		} else if ref == "" {
			ref = "HEAD"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Source, ref, shortCommit(e.Commit))
	}
	return tw.Flush()
}

func moduleUpdateCommand(args []string, in io.Reader, w io.Writer) error {
	var yes bool
	pos, project, err := moduleArgs(args, map[string]*bool{"--yes": &yes, "-y": &yes})
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fmt.Errorf("usage: cloche module update <name> [<ref>] [--yes]")
	}
	newRef := ""
	if len(pos) == 2 {
		newRef = pos[1]
	}
	plan, err := module.PlanUpdate(project, pos[0], newRef)
	if err != nil {
		return err
	}
	defer plan.Close()

	if plan.LocalEdit {
		fmt.Fprintf(w, "warning: .cloche/modules/%s has local edits; updating will overwrite them\n", plan.New.Name)
	}
	if plan.Diff == "" && plan.New.Commit == plan.Entry.Commit {
		fmt.Fprintf(w, "Module %s is up to date (%s).\n", plan.New.Name, shortCommit(plan.New.Commit))
		if !plan.LocalEdit {
			return nil
		}
	}
	fmt.Fprintf(w, "Update %s: %s -> %s\n\n", plan.New.Name, shortCommit(plan.Entry.Commit), shortCommit(plan.New.Commit))
	if plan.Diff == "" {
		fmt.Fprintln(w, "(no file changes)")
	} else {
		fmt.Fprintln(w, plan.Diff)
	}
	if !yes {
		fmt.Fprint(w, "Apply this update? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(w, "\nAborted; module unchanged.")
			return nil
		}
	}
	if err := plan.Apply(project); err != nil {
		return err
	}
	fmt.Fprintf(w, "Updated module %s to %s. Review and commit the changes.\n", plan.New.Name, shortCommit(plan.New.Commit))
	return nil
}

func moduleRemoveCommand(args []string, w io.Writer) error {
	pos, project, err := moduleArgs(args, nil)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: cloche module remove <name>")
	}
	e, err := module.Remove(project, pos[0])
	if err != nil {
		return err
	}
	if _, local := e.LocalPath(); local {
		fmt.Fprintf(w, "Removed module %s from modules.lock (local directory left untouched).\n", e.Name)
	} else {
		fmt.Fprintf(w, "Removed module %s and .cloche/modules/%s/.\n", e.Name, e.Name)
	}
	return nil
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	if c == "" {
		return "-"
	}
	return c
}
