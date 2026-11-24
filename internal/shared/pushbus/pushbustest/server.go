// Package pushbustest runs the deployment's NATS configuration in process
// with synthetic certificates, for tests of the push signal stream.
package pushbustest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
)

// Authority is a synthetic deployment CA.
type Authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// NewAuthority creates a CA valid for an hour.
func NewAuthority(t testing.TB) *Authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test internal CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	must(t, err)
	cert, err = x509.ParseCertificate(der)
	must(t, err)
	return &Authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// Issue writes ca.pem, tls.crt and tls.key for service into a private
// directory, with the SANs of scripts/gen-rpc-certs.py.
func (a *Authority) Issue(t testing.TB, service string) string {
	t.Helper()
	dir := privateDir(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	uri, err := url.Parse("spiffe://vault-chat/service/" + service)
	must(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	must(t, err)
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: service},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{service + ".vault.internal"}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, a.cert, &key.PublicKey, a.key)
	must(t, err)
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	must(t, err)
	for name, data := range map[string][]byte{
		"ca.pem":  a.pem,
		"tls.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}),
	} {
		must(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
	}
	return dir
}

// TLS loads the internal TLS configuration of service from dir, as the
// service does with RPC_TLS_MODE=mtls.
func TLS(t testing.TB, service, dir string) *rpctls.Config {
	t.Helper()
	t.Setenv("RPC_TLS_MODE", "mtls")
	t.Setenv("RPC_TLS_DIR", dir)
	cfg, err := rpctls.Load(service)
	must(t, err)
	return cfg
}

// Server is a NATS server started from deployments/nats/nats.conf.
type Server struct {
	CA    *Authority
	opts  *server.Options
	srv   *server.Server
	store string
	t     testing.TB
}

// Start runs the deployment configuration with a fresh CA, a server
// certificate for "nats", a random port and a private store directory.
func Start(t testing.TB) *Server {
	t.Helper()
	ca := NewAuthority(t)
	s := &Server{CA: ca, store: privateDir(t), t: t}
	certs := ca.Issue(t, "nats")
	config, err := os.ReadFile(ConfigPath(t))
	must(t, err)
	text := strings.ReplaceAll(string(config), "/run/secrets/nats/", filepath.ToSlash(certs)+"/")
	text = strings.ReplaceAll(text, "store_dir: /data", "store_dir: \""+filepath.ToSlash(s.store)+"\"")
	path := filepath.Join(privateDir(t), "nats.conf")
	must(t, os.WriteFile(path, []byte(text), 0o600))
	opts, err := server.ProcessConfigFile(path)
	must(t, err)
	opts.Host, opts.Port = "127.0.0.1", -1
	opts.HTTPHost, opts.HTTPPort = "", 0
	opts.NoLog, opts.NoSigs = true, true
	s.opts = opts
	s.start()
	t.Cleanup(s.Shutdown)
	return s
}

func (s *Server) start() {
	s.t.Helper()
	srv, err := server.NewServer(s.opts)
	must(s.t, err)
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		s.t.Fatal("NATS test server did not start")
	}
	s.srv = srv
	// Keep the port across restarts, so that clients reconnect.
	s.opts.Port = srv.Addr().(*net.TCPAddr).Port
}

// URL is the client URL of the running server.
func (s *Server) URL() string { return "tls://127.0.0.1:" + strconv.Itoa(s.opts.Port) }

// Shutdown stops the server; Restart starts it again with the same store.
func (s *Server) Shutdown() {
	if s.srv != nil {
		s.srv.Shutdown()
		s.srv.WaitForShutdown()
		s.srv = nil
	}
}

// Restart starts a stopped server on the same port and store.
func (s *Server) Restart() {
	s.t.Helper()
	s.Shutdown()
	s.start()
}

// RestartEmpty starts the server again on the same port with an empty
// store, as after losing the volume: the stream is gone.
func (s *Server) RestartEmpty() {
	s.t.Helper()
	s.Shutdown()
	s.store = privateDir(s.t)
	s.opts.StoreDir = s.store
	s.start()
}

// ConfigPath is the deployment's NATS configuration file.
func ConfigPath(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate pushbustest source")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "deployments", "nats", "nats.conf")
}

func privateDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	must(t, os.Chmod(dir, 0o700))
	return dir
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
