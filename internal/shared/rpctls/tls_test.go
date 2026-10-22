package rpctls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newAuthority(t *testing.T) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)
	return authority{cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}
func (a authority) issue(t *testing.T, service string, expired bool) string {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	uri, err := url.Parse("spiffe://vault-chat/service/" + service)
	require.NoError(t, err)
	end := time.Now().Add(time.Hour)
	if expired {
		end = time.Now().Add(-time.Minute)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), NotBefore: time.Now().Add(-time.Hour), NotAfter: end, DNSNames: []string{service + ".vault.internal"}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, a.cert, &key.PublicKey, a.key)
	require.NoError(t, err)
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	for name, data := range map[string][]byte{"ca.pem": a.pem, "tls.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0600))
	}
	return dir
}
func loadTest(t *testing.T, service, dir string) *Config {
	t.Helper()
	t.Setenv("RPC_TLS_MODE", "mtls")
	t.Setenv("RPC_TLS_DIR", dir)
	c, err := Load(service)
	require.NoError(t, err)
	return c
}
func TestMutualTLSOverTCP(t *testing.T) {
	a := newAuthority(t)
	serverConfig := loadTest(t, "auth", a.issue(t, "auth", false))
	gateway := loadTest(t, "gateway", a.issue(t, "gateway", false))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(serverConfig.ServerOption(), grpc.ChainUnaryInterceptor(serverConfig.Unary()), grpc.ChainStreamInterceptor(serverConfig.Stream()))
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(l) }()
	t.Cleanup(server.Stop)
	for _, tt := range []struct {
		name, target string
		cfg          *Config
		ok           bool
	}{{"valid", "auth", gateway, true}, {"wrong server identity", "chat", gateway, false}, {"foreign CA", "auth", loadTest(t, "gateway", newAuthority(t).issue(t, "gateway", false)), false}, {"expired client", "auth", loadTest(t, "gateway", a.issue(t, "gateway", true)), false}} {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := tt.cfg.Dial(l.Addr().String(), tt.target)
			require.NoError(t, err)
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
			if tt.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	// A CA-trusting client without its own certificate must still be rejected.
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(a.pem))
	conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "auth.vault.internal"})))
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	require.Error(t, err)
}
func TestFailClosedConfiguration(t *testing.T) {
	t.Setenv("RPC_TLS_MODE", "mtls")
	t.Setenv("RPC_TLS_DIR", t.TempDir())
	_, err := Load("auth")
	require.Error(t, err)
	t.Setenv("RPC_TLS_MODE", "typo")
	_, err = Load("auth")
	require.Error(t, err)
	t.Setenv("RPC_TLS_MODE", "plaintext")
	_, err = Load("auth")
	require.Error(t, err, "TLS files must not be silently ignored")
	a := newAuthority(t)
	t.Setenv("RPC_TLS_MODE", "mtls")
	t.Setenv("RPC_TLS_DIR", a.issue(t, "chat", false))
	_, err = Load("auth")
	require.Error(t, err, "local identity must match service")
}
func TestRPCPermissions(t *testing.T) {
	a := newAuthority(t)
	c := loadTest(t, "auth", a.issue(t, "auth", false))
	for _, tt := range []struct {
		service, method string
		ok              bool
	}{{"gateway", "/auth.v1.AuthService/Login", true}, {"chat", "/auth.v1.AuthService/ValidateToken", true}, {"chat", "/auth.v1.AuthService/Login", false}, {"media", "/chat.v1.ChatService/GetRoom", false}, {"auth", "/auth.v1.AuthService/Login", false}} {
		dir := a.issue(t, tt.service, false)
		pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
		require.NoError(t, err)
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		require.NoError(t, err)
		ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{cert}}}})
		called := false
		_, err = c.Unary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method}, func(context.Context, any) (any, error) { called = true; return nil, nil })
		require.Equal(t, tt.ok, called)
		if !tt.ok {
			require.Equal(t, codes.PermissionDenied, status.Code(err))
		}
	}
	_, err := c.Unary()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/auth.v1.AuthService/Login"}, func(context.Context, any) (any, error) { t.Fatal("missing identity passed"); return nil, nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
func FuzzIdentity(f *testing.F) {
	for _, s := range []string{"spiffe://vault-chat/service/auth", "spiffe://evil/service/auth", "", "spiffe://vault-chat/service/auth?admin=1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := url.Parse(raw)
		if err != nil {
			return
		}
		name := identity(&x509.Certificate{URIs: []*url.URL{u}})
		if name != "" {
			require.Contains(t, []string{"auth", "chat", "media", "gateway"}, name)
			require.Equal(t, "spiffe://vault-chat/service/"+name, raw)
		}
	})
}
