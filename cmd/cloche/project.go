package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/projectcli"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func cmdProject(args []string) {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}
	if err := projectCommand(args, addr, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// projectCommand executes the project command, writing output to w.
// Extracted from cmdProject so BDD tests can call it without os.Exit.
func projectCommand(args []string, addr string, w io.Writer) error {
	// Check for subcommands first.
	if len(args) >= 2 && args[0] == "repos" && args[1] == "list" {
		return projectReposListCommand(args[2:], addr, w)
	}

	var name string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		}
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetProjectInfoRequest{}
	if name != "" {
		req.Name = name
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		req.ProjectDir = cwd
	}

	resp, err := client.GetProjectInfo(ctx, req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	projectcli.WriteProjectInfo(resp, w)
	return nil
}

// projectReposListCommand implements "cloche project repos list".
func projectReposListCommand(args []string, addr string, w io.Writer) error {
	var name string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		}
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetProjectInfoRequest{}
	if name != "" {
		req.Name = name
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		req.ProjectDir = cwd
	}

	resp, err := client.GetProjectInfo(ctx, req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	projectcli.WriteReposList(resp.Repositories, w)
	return nil
}

