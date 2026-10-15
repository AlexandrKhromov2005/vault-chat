// Package probe implements the container health probes used by Docker
// Compose. Runtime images are distroless and have no shell, curl or
// grpc_health_probe, so the checks are compiled into a tiny Go binary.
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HTTP reports whether a GET request to url answers with a 2xx status.
// Redirects are not followed: a health endpoint must answer directly.
func HTTP(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}

// GRPC reports whether the standard gRPC health service at addr says the
// server as a whole is SERVING. Internal traffic is plaintext until mTLS
// lands (ARCHITECTURE.md section 3.3), so the probe connects without TLS.
func GRPC(ctx context.Context, addr string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("create health client for %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("check %s: %w", addr, err)
	}
	if status := resp.GetStatus(); status != healthpb.HealthCheckResponse_SERVING {
		return errors.New(addr + " is " + status.String())
	}
	return nil
}
