package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

// cmdThreads dispatches `cloche threads [list|show|reply]`. Bare `cloche
// threads` is shorthand for `cloche threads list`.
func cmdThreads(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) == 0 {
		cmdThreadsList(ctx, client, nil)
		return
	}

	switch args[0] {
	case "list":
		cmdThreadsList(ctx, client, args[1:])
	case "show":
		cmdThreadsShow(ctx, client, args[1:])
	case "reply":
		cmdThreadsReply(ctx, client, args[1:])
	default:
		// No recognized subcommand: treat as `threads list` filtered flags,
		// unless it looks like someone forgot the verb (e.g. "cloche threads --all").
		if strings.HasPrefix(args[0], "-") {
			cmdThreadsList(ctx, client, args)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown threads subcommand: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "usage: cloche threads [list [--all] [--channel <c>] | show <channel>/<name> | reply <channel>/<name> \"message\"]\n")
		os.Exit(1)
	}
}

func cmdThreadsList(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var all bool
	var channel string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			all = true
		case "--channel":
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "--channel requires a value\n")
				os.Exit(1)
			}
			channel = args[i]
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	resp, err := client.ListThreads(ctx, &pb.ListThreadsRequest{All: all, Channel: channel})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Threads) == 0 {
		fmt.Println("No open threads.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tSTATE\tTASK ID\tCREATED\tTITLE")
	for _, t := range resp.Threads {
		title := t.Title
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		address := t.Channel + "/" + t.Name
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			colorID(address), colorStatus(t.State), t.TaskId, t.CreatedAt, title)
	}
	w.Flush()
}

func cmdThreadsShow(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche threads show <channel>/<name>\n")
		os.Exit(1)
	}

	resp, err := client.GetThread(ctx, &pb.GetThreadRequest{Address: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	t := resp.Thread
	fmt.Printf("Thread:  %s\n", colorID(t.Channel+"/"+t.Name))
	fmt.Printf("Title:   %s\n", t.Title)
	fmt.Printf("State:   %s\n", colorStatus(t.State))
	if t.TaskId != "" {
		fmt.Printf("Task:    %s\n", colorID(t.TaskId))
	}
	if t.RunId != "" {
		fmt.Printf("Run:     %s\n", t.RunId)
	}
	if t.StepName != "" {
		fmt.Printf("Step:    %s\n", t.StepName)
	}
	fmt.Println()

	for _, m := range resp.Messages {
		author := "agent"
		if m.Author == "user" {
			author = "user"
		}
		fmt.Printf("[%s] %s:\n", m.CreatedAt, author)
		fmt.Printf("  %s\n", m.Body)
		if len(m.Options) > 0 {
			fmt.Printf("  options: %s\n", strings.Join(m.Options, ", "))
		}
		fmt.Println()
	}
}

func cmdThreadsReply(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: cloche threads reply <channel>/<name> \"message\"\n")
		os.Exit(1)
	}

	address := args[0]
	body := strings.Join(args[1:], " ")

	_, err := client.ReplyThread(ctx, &pb.ReplyThreadRequest{Address: address, Body: body})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Reply sent to %s.\n", colorID(address))
}
