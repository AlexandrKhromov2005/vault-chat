// Package rpctls authenticates internal gRPC peers and restricts their RPCs.
package rpctls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/requestid"
)

// Config holds an immutable certificate snapshot. Rotate leaves by restarting processes.
type Config struct {
	service string
	tls     *tls.Config
	expiry  float64
}

// Load permits plaintext only when explicitly selected or in the default local setup.
// Any partially configured TLS mode fails startup instead of downgrading.
func Load(service string) (*Config, error) {
	mode, dir := os.Getenv("RPC_TLS_MODE"), os.Getenv("RPC_TLS_DIR")
	c := &Config{service: service}
	if mode == "" || mode == "plaintext" {
		if dir != "" {
			return nil, errors.New("RPC_TLS_DIR requires RPC_TLS_MODE=mtls")
		}
		slog.Warn("internal RPC TLS disabled; use only in local development")
		return c, nil
	}
	if mode != "mtls" || !known(service) || dir == "" {
		return nil, errors.New("invalid RPC TLS configuration")
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, fmt.Errorf("read RPC CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid RPC CA PEM")
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		return nil, fmt.Errorf("load RPC certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if identity(leaf) != service {
		return nil, errors.New("RPC certificate does not identify this service")
	}
	c.expiry = float64(leaf.NotAfter.Unix())
	c.tls = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
	return c, nil
}

// Enabled reports whether mutual TLS and RPC authorization are enabled.
func (c *Config) Enabled() bool { return c.tls != nil }

// CertificateExpiry is the loaded certificate expiry, or zero in plaintext mode.
func (c *Config) CertificateExpiry() float64 { return c.expiry }
func known(s string) bool                    { return s == "auth" || s == "chat" || s == "media" || s == "gateway" }
func identity(cert *x509.Certificate) string {
	if len(cert.URIs) != 1 {
		return ""
	}
	for _, s := range []string{"auth", "chat", "media", "gateway"} {
		if cert.URIs[0].String() == "spiffe://vault-chat/service/"+s {
			return s
		}
	}
	return ""
}

// Dial verifies both the CA/DNS name and the exact service URI in the server leaf.
func (c *Config) Dial(addr, target string) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if c.Enabled() {
		if !known(target) {
			return nil, errors.New("unknown RPC target identity")
		}
		cfg := c.tls.Clone()
		cfg.ServerName = target + ".vault.internal"
		cfg.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || identity(state.PeerCertificates[0]) != target {
				return errors.New("unexpected RPC server identity")
			}
			return nil
		}
		creds = credentials.NewTLS(cfg)
	}
	return grpc.NewClient(addr, grpc.WithTransportCredentials(creds), grpc.WithUnaryInterceptor(requestid.UnaryClientInterceptor()))
}

// ServerOption requires a client certificate issued by this deployment's private CA.
func (c *Config) ServerOption() grpc.ServerOption {
	if !c.Enabled() {
		return grpc.EmptyServerOption{}
	}
	cfg := c.tls.Clone()
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.VerifiedChains) == 0 || identity(state.PeerCertificates[0]) == "" {
			return errors.New("unknown RPC client identity")
		}
		return nil
	}
	return grpc.Creds(credentials.NewTLS(cfg))
}
func (c *Config) authorize(ctx context.Context, method string) error {
	if !c.Enabled() {
		return nil
	}
	p, ok := peer.FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "RPC peer certificate required")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.PeerCertificates) == 0 {
		return status.Error(codes.Unauthenticated, "verified RPC certificate required")
	}
	caller := identity(info.State.PeerCertificates[0])
	if caller == "" {
		return status.Error(codes.Unauthenticated, "unknown RPC identity")
	}
	allowed := method == "/grpc.health.v1.Health/Check" || method == "/grpc.health.v1.Health/Watch"
	if caller == "gateway" {
		allowed = allowed || strings.HasPrefix(method, "/"+c.service+".v1."+strings.ToUpper(c.service[:1])+c.service[1:]+"Service/")
	}
	switch c.service {
	case "auth":
		allowed = allowed || (caller == "chat" && (method == "/auth.v1.AuthService/ValidateToken" || method == "/auth.v1.AuthService/GetUser")) || (caller == "media" && method == "/auth.v1.AuthService/ValidateToken")
	case "chat":
		allowed = allowed || (caller == "media" && method == "/chat.v1.ChatService/GetRoom")
	case "media":
		allowed = allowed || (caller == "chat" && method == "/media.v1.MediaService/GetFile")
	}
	if !allowed {
		return status.Error(codes.PermissionDenied, "RPC not permitted for this service")
	}
	return nil
}

// Unary authorizes service identity in addition to the handler's user token checks.
func (c *Config) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if err := c.authorize(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// Stream protects streaming uploads/downloads and prevents reflection bypasses.
func (c *Config) Stream() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if err := c.authorize(stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return next(srv, stream)
	}
}
