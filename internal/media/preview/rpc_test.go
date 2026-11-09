package preview

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"image/color"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	previewv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/preview/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
)

type decoderFunc func(context.Context, []byte) (Result, error)

func (f decoderFunc) Decode(ctx context.Context, data []byte) (Result, error) {
	return f(ctx, data)
}

func TestRPCHandlerMapsResultsAndFailures(t *testing.T) {
	data := encodePNG(t, solid(2, 1, color.NRGBA{A: 255}))
	for name, test := range map[string]struct {
		decoder Decoder
		code    codes.Code
	}{
		"invalid": {decoderFunc(func(context.Context, []byte) (Result, error) { return Result{}, ErrInvalid }), codes.InvalidArgument},
		"skipped": {decoderFunc(func(context.Context, []byte) (Result, error) { return Result{}, ErrSkipped }), codes.OutOfRange},
		"worker":  {decoderFunc(func(context.Context, []byte) (Result, error) { return Result{}, ErrWorker }), codes.Internal},
		"bad response": {decoderFunc(func(context.Context, []byte) (Result, error) {
			return Result{Width: MaxSide + 1, Height: 1}, nil
		}), codes.Internal},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewRPCHandler(test.decoder).Decode(context.Background(), &previewv1.DecodeRequest{Image: data})
			if status.Code(err) != test.code {
				t.Fatalf("code %s, want %s: %v", status.Code(err), test.code, err)
			}
		})
	}
}

func TestRPCHandlerAdmitsOneDecode(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := NewRPCHandler(decoderFunc(func(context.Context, []byte) (Result, error) {
		close(started)
		<-release
		return Result{Width: 2, Height: 1, RGB: make([]byte, 6)}, nil
	}))
	first := make(chan error, 1)
	go func() {
		_, err := handler.Decode(context.Background(), &previewv1.DecodeRequest{Image: []byte("first")})
		first <- err
	}()
	<-started
	_, err := handler.Decode(context.Background(), &previewv1.DecodeRequest{Image: []byte("second")})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("concurrent decode: %v, want Unavailable", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestRPCRequestSizeLimit(t *testing.T) {
	var received int
	handler := NewRPCHandler(decoderFunc(func(_ context.Context, data []byte) (Result, error) {
		received = len(data)
		return Result{}, ErrInvalid
	}))
	addr := startTestRPCServer(t, handler, nil)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := previewv1.NewPreviewServiceClient(conn)
	_, err = client.Decode(context.Background(), &previewv1.DecodeRequest{Image: make([]byte, MaxInput)})
	if status.Code(err) != codes.InvalidArgument || received != MaxInput {
		t.Fatalf("exact limit: received %d, %v", received, err)
	}
	_, err = client.Decode(context.Background(), &previewv1.DecodeRequest{Image: make([]byte, MaxInput+1)})
	if status.Code(err) != codes.OutOfRange {
		t.Fatalf("image over budget: %v, want OutOfRange", err)
	}
	_, err = client.Decode(context.Background(), &previewv1.DecodeRequest{Image: make([]byte, MaxRPCRequest+1)})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized wire request: %v, want ResourceExhausted", err)
	}
}

func TestRPCClientMapsDependencyOutage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = NewRPCClient(conn).Decode(ctx, encodePNG(t, solid(2, 1, color.NRGBA{A: 255})))
	if !errors.Is(err, ErrWorker) {
		t.Fatalf("outage: %v, want ErrWorker", err)
	}
}

func TestPreviewMutualTLSAndAuthorization(t *testing.T) {
	a := newPreviewAuthority(t)
	serverTLS := loadPreviewTLS(t, "preview", a.issue(t, "preview"))
	mediaTLS := loadPreviewTLS(t, "media", a.issue(t, "media"))
	gatewayTLS := loadPreviewTLS(t, "gateway", a.issue(t, "gateway"))
	foreignTLS := loadPreviewTLS(t, "media", newPreviewAuthority(t).issue(t, "media"))
	handler := NewRPCHandler(decoderFunc(func(_ context.Context, data []byte) (Result, error) {
		w, h, rgb, err := DecodeRGB(data)
		return Result{Width: w, Height: h, RGB: rgb}, err
	}))
	addr := startTestRPCServer(t, handler, serverTLS)

	conn, err := mediaTLS.Dial(addr, "preview")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	got, err := NewRPCClient(conn).Decode(context.Background(), encodePNG(t, solid(2, 1, color.NRGBA{A: 255})))
	if err != nil || got.Width != 2 || got.Height != 1 || len(got.RGB) != 6 {
		t.Fatalf("Media decode: %+v, %v", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Media health: %v", err)
	}

	gatewayConn, err := gatewayTLS.Dial(addr, "preview")
	if err != nil {
		t.Fatal(err)
	}
	defer gatewayConn.Close()
	_, err = previewv1.NewPreviewServiceClient(gatewayConn).Decode(ctx, &previewv1.DecodeRequest{Image: []byte("denied")})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Gateway decode: %v, want PermissionDenied", err)
	}

	foreignConn, err := foreignTLS.Dial(addr, "preview")
	if err != nil {
		t.Fatal(err)
	}
	defer foreignConn.Close()
	_, err = previewv1.NewPreviewServiceClient(foreignConn).Decode(ctx, &previewv1.DecodeRequest{Image: []byte("denied")})
	if err == nil {
		t.Fatal("foreign CA decoded an image")
	}
}

func startTestRPCServer(t *testing.T, handler *RPCHandler, rpcTLS *rpctls.Config) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var options []grpc.ServerOption
	if rpcTLS != nil {
		options = append(options, rpcTLS.ServerOption(), grpc.ChainUnaryInterceptor(rpcTLS.Unary()), grpc.ChainStreamInterceptor(rpcTLS.Stream()))
	}
	server := NewRPCServer(options...)
	previewv1.RegisterPreviewServiceServer(server, handler)
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

type previewAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newPreviewAuthority(t *testing.T) previewAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "preview test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return previewAuthority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (a previewAuthority) issue(t *testing.T, service string) string {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse("spiffe://vault-chat/service/" + service)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{service + ".vault.internal"}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"ca.pem": a.pem, "tls.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadPreviewTLS(t *testing.T, service, dir string) *rpctls.Config {
	t.Helper()
	t.Setenv("RPC_TLS_MODE", "mtls")
	t.Setenv("RPC_TLS_DIR", dir)
	cfg, err := rpctls.Load(service)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
