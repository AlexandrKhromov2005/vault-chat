// Command healthcheck probes a Vault Chat service from inside its container.
// Runtime images are distroless, so Compose healthchecks run this binary:
//
//	healthcheck -http http://127.0.0.1:8080/health
//	healthcheck -grpc 127.0.0.1:50051
//
// It exits 0 when the target is healthy, 1 when it is not and 2 on misuse.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/probe"
)

const (
	exitHealthy   = 0
	exitUnhealthy = 1
	exitUsage     = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	httpURL := flags.String("http", "", "URL whose GET must answer 2xx")
	grpcAddr := flags.String("grpc", "", "host:port of a gRPC server with the standard health service")
	timeout := flags.Duration("timeout", 3*time.Second, "overall probe deadline")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 || (*httpURL == "") == (*grpcAddr == "") || *timeout <= 0 {
		_, _ = fmt.Fprintln(stderr, "usage: healthcheck (-http URL | -grpc HOST:PORT) [-timeout DURATION]")
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var err error
	if *httpURL != "" {
		err = probe.HTTP(ctx, *httpURL)
	} else {
		err = probe.GRPC(ctx, *grpcAddr)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "unhealthy:", err)
		return exitUnhealthy
	}
	return exitHealthy
}
