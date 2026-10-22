// Package telemetry exposes process-local Prometheus registries on a separate listener.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Metrics belongs to one process. Request payloads and identifiers never become labels.
type Metrics struct {
	registry    *prometheus.Registry
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	rpcRequests *prometheus.CounterVec
	rpcDuration *prometheus.HistogramVec
}

// New makes an isolated registry; service is a fixed application name.
func New(service string) *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry(),
		requests:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vault_http_requests_total", Help: "Completed HTTP requests."}, []string{"kind", "method", "status"}),
		duration:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vault_http_request_duration_seconds", Help: "HTTP request duration, excluding WebSocket lifetime.", Buckets: prometheus.DefBuckets}, []string{"kind", "method"}),
		rpcRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vault_grpc_requests_total", Help: "Completed unary RPC requests."}, []string{"method", "code"}),
		rpcDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vault_grpc_request_duration_seconds", Help: "Unary RPC duration.", Buckets: prometheus.DefBuckets}, []string{"method"}),
	}
	info := prometheus.NewGauge(prometheus.GaugeOpts{Name: "vault_service_info", Help: "Application service identity.", ConstLabels: prometheus.Labels{"service": service}})
	info.Set(1)
	m.registry.MustRegister(info, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), m.requests, m.duration, m.rpcRequests, m.rpcDuration)
	return m
}

// Gauge reads an existing concurrency-safe application gauge at scrape time.
func (m *Metrics) Gauge(name, help string, read func() float64) {
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, read))
}

// Counter reads an existing monotonic application counter at scrape time.
func (m *Metrics) Counter(name, help string, read func() float64) {
	m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, read))
}

// Handler serves Prometheus text format without adding public application routes.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

type recorder struct {
	http.ResponseWriter
	code int
}

func (w *recorder) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *recorder) Write(data []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.ResponseWriter.Write(data)
}

// Unwrap preserves ResponseController operations, including WebSocket hijacking.
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// HTTP records bounded request categories rather than raw paths, IDs or queries.
func (m *Metrics) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := "other"
		switch {
		case r.URL.Path == "/api/v1/ws":
			kind = "websocket"
		case strings.HasPrefix(r.URL.Path, "/api/"):
			kind = "api"
		case r.URL.Path == "/health" || r.URL.Path == "/ready":
			kind = "probe"
		case r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/app/"):
			kind = "static"
		}
		method := r.Method
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			method = "OTHER"
		}
		started := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		code := rec.code
		if code == 0 {
			code = 200
		}
		m.requests.WithLabelValues(kind, method, strconv.Itoa(code)).Inc()
		if kind != "websocket" {
			m.duration.WithLabelValues(kind, method).Observe(time.Since(started).Seconds())
		}
	})
}

// Unary instruments registered RPC methods; request contents and error messages are ignored.
func (m *Metrics) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		start := time.Now()
		result, err := next(ctx, req)
		m.rpcRequests.WithLabelValues(info.FullMethod, status.Code(err).String()).Inc()
		m.rpcDuration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		return result, err
	}
}

// Listen binds the private metrics listener, or disables exposure when addr is empty.
// The caller controls its address; Compose exposes it only to the monitoring stack.
func (m *Metrics) Listen(ctx context.Context, addr string) (func(), error) {
	if addr == "" {
		return func() {}, nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics listener stopped", "error", err)
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	return func() { _ = server.Close(); <-done }, nil
}
