package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"google.golang.org/grpc"
)

func main() {
	dbPath := os.Getenv("CLOCHE_DB")
	if dbPath == "" {
		dbPath = "cloche.db"
	}

	listenAddr := os.Getenv("CLOCHE_LISTEN")
	if listenAddr == "" {
		listenAddr = "unix:///tmp/cloche.sock"
	}

	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	srv := adaptgrpc.NewClocheServer(store, nil)

	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, srv)

	lis, err := listen(listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen on %s: %v\n", listenAddr, err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		grpcServer.GracefulStop()
	}()

	fmt.Fprintf(os.Stderr, "cloched listening on %s\n", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func listen(addr string) (net.Listener, error) {
	if len(addr) > 7 && addr[:7] == "unix://" {
		sockPath := addr[7:]
		os.Remove(sockPath)
		return net.Listen("unix", sockPath)
	}
	return net.Listen("tcp", addr)
}
