package mail

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"mime"
	"net"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

type smtpCapture struct {
	commands   []string
	payload    []byte
	tlsVersion uint16
}

func smtpFixture(t *testing.T, implicit, offerTLS bool, reject ...string) (string, *tls.Config, <-chan smtpCapture) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic SMTP"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	result := make(chan smtpCapture, 1)
	t.Cleanup(func() { close(stop); _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		capture := smtpCapture{}
		defer func() { result <- capture }()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		raw := conn
		defer func() { _ = raw.Close() }()
		go func() { <-stop; _ = raw.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		secure := implicit
		if secure {
			tlsConn := tls.Server(conn, serverTLS)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			capture.tlsVersion = tlsConn.ConnectionState().Version
			conn = tlsConn
		}
		reader := bufio.NewReader(conn)
		write := func(value string) bool { _, err := io.WriteString(conn, value); return err == nil }
		if !write("220 synthetic.example.invalid ESMTP\r\n") {
			return
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			command := strings.TrimSpace(line)
			// Retain command verbs, never authentication material.
			verb := strings.SplitN(command, " ", 2)[0]
			capture.commands = append(capture.commands, verb)
			if len(reject) > 0 && verb == reject[0] {
				write("550 synthetic-secret receiver@example.invalid\r\n")
				return
			}
			switch verb {
			case "EHLO":
				if !write("250-synthetic\r\n") {
					return
				}
				if !secure && offerTLS && !write("250-STARTTLS\r\n") {
					return
				}
				if !write("250 AUTH PLAIN\r\n") {
					return
				}
			case "STARTTLS":
				if !write("220 upgrade\r\n") {
					return
				}
				tlsConn := tls.Server(conn, serverTLS)
				if err := tlsConn.Handshake(); err != nil {
					return
				}
				capture.tlsVersion = tlsConn.ConnectionState().Version
				conn, secure = tlsConn, true
				reader = bufio.NewReader(conn)
			case "AUTH", "MAIL", "RCPT":
				if !secure {
					return
				}
				if verb == "AUTH" {
					if !write("235 authenticated\r\n") {
						return
					}
				} else if !write("250 accepted\r\n") {
					return
				}
			case "DATA":
				if !secure || !write("354 message\r\n") {
					return
				}
				capture.payload, err = textproto.NewReader(reader).ReadDotBytes()
				if err != nil || !write("250 queued\r\n") {
					return
				}
			case "QUIT":
				write("221 bye\r\n")
				return
			default:
				return
			}
		}
	}()
	return listener.Addr().String(), &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, result
}

func TestSMTPRequiresTLSAndDeliversUTF8MIME(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "STARTTLS", true: "implicit"}[implicit], func(t *testing.T) {
			address, config, received := smtpFixture(t, implicit, true)
			sender, err := NewSMTP(SMTPConfig{Address: address, From: "Хранилище <sender@example.invalid>", Username: "synthetic-user", Password: "synthetic-password", ImplicitTLS: implicit, TLSConfig: config})
			if err != nil {
				t.Fatal(err)
			}
			message := Message{To: "receiver@example.invalid", Subject: "Проверка адреса", Text: strings.Repeat("Код: synthetic-token\nSecond line\n", 20)}
			if err := sender.Send(context.Background(), message); err != nil {
				t.Fatal(err)
			}
			capture := <-received
			if capture.tlsVersion < tls.VersionTLS12 {
				t.Fatal("SMTP delivery did not use TLS 1.2+")
			}
			if strings.Join(capture.commands, ",") == "" {
				t.Fatal("no SMTP conversation")
			}
			auth, recipientsSent := 0, 0
			for _, command := range capture.commands {
				if command == "AUTH" {
					auth++
				}
				if command == "RCPT" {
					recipientsSent++
				}
			}
			if auth != 1 || recipientsSent != 1 {
				t.Fatal("wrong authentication/envelope recipient count")
			}
			parsed, err := stdmail.ReadMessage(strings.NewReader(string(capture.payload)))
			if err != nil {
				t.Fatal(err)
			}
			subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
			if err != nil || subject != message.Subject {
				t.Fatal("UTF-8 subject was not MIME encoded")
			}
			from, err := stdmail.ParseAddress(parsed.Header.Get("From"))
			if err != nil || from.Name != "Хранилище" {
				t.Fatal("UTF-8 sender name was not MIME encoded")
			}
			body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, parsed.Body))
			if err != nil || string(body) != message.Text {
				t.Fatalf("UTF-8 body changed: %v", err)
			}
			recipients, err := stdmail.ParseAddressList(parsed.Header.Get("To"))
			if err != nil || len(recipients) != 1 || recipients[0].Address != message.To {
				t.Fatal("wrong recipients")
			}
		})
	}
}

func TestSMTPRejectsUntrustedCertificateAndTLSStripping(t *testing.T) {
	for _, offerTLS := range []bool{false, true} {
		address, _, received := smtpFixture(t, false, offerTLS)
		sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.invalid", Username: "synthetic-user", Password: "synthetic-password"})
		if err != nil {
			t.Fatal(err)
		}
		if err := sender.Send(context.Background(), Message{To: "receiver@example.invalid", Subject: "Verify", Text: "secret"}); err == nil {
			t.Fatal("accepted insecure SMTP")
		}
		capture := <-received
		for _, command := range capture.commands {
			if command == "AUTH" || command == "MAIL" || command == "DATA" {
				t.Fatal("sent credentials/message before verified TLS")
			}
		}
	}
}

func TestSMTPRejectsInjectionAndUnsafeConfiguration(t *testing.T) {
	for _, config := range []SMTPConfig{
		{Address: "smtp.example.invalid:587", From: "sender@example.invalid\r\nBcc: target@example.invalid"},
		{Address: "smtp.example.invalid:587", From: "sender@example.invalid", TLSConfig: &tls.Config{InsecureSkipVerify: true}}, // deliberate negative security fixture
		{Address: "smtp.example.invalid:587", From: "sender@example.invalid", TLSConfig: &tls.Config{MinVersion: tls.VersionTLS10}},
		{Address: "smtp.example.invalid:587", From: "sender@example.invalid", Username: "user"},
	} {
		if _, err := NewSMTP(config); err == nil {
			t.Fatal("accepted unsafe SMTP configuration")
		}
	}
	for _, message := range []Message{
		{To: "receiver@example.invalid\r\nRCPT TO:target@example.invalid", Subject: "Verify", Text: "body"},
		{To: "receiver@example.invalid", Subject: "Verify\r\nBcc: target@example.invalid", Text: "body"},
	} {
		address, config, _ := smtpFixture(t, false, true)
		sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.invalid", TLSConfig: config})
		if err != nil {
			t.Fatal(err)
		}
		if err := sender.Send(context.Background(), message); err == nil {
			t.Fatal("accepted header injection")
		}
	}
}

func TestSMTPCancellationInterruptsStalledServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, conn)
	}()
	sender, err := NewSMTP(SMTPConfig{Address: listener.Addr().String(), From: "sender@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := sender.Send(ctx, Message{To: "receiver@example.invalid", Subject: "Verify", Text: "body"}); err == nil {
		t.Fatal("stalled SMTP succeeded")
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("SMTP ignored cancellation")
	}
	_ = listener.Close()
	<-done
}

func TestSMTPReplyErrorsAreOpaqueAndAcceptedDataIsNotRetriedOnQuitFailure(t *testing.T) {
	for _, rejected := range []string{"MAIL", "RCPT", "DATA", "QUIT"} {
		t.Run(rejected, func(t *testing.T) {
			address, config, _ := smtpFixture(t, false, true, rejected)
			sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.invalid", TLSConfig: config})
			if err != nil {
				t.Fatal(err)
			}
			err = sender.Send(context.Background(), Message{To: "receiver@example.invalid", Subject: "Verify", Text: "synthetic-token"})
			if rejected == "QUIT" {
				if err != nil {
					t.Fatal("accepted DATA must not become a duplicate retry")
				}
			} else if err == nil || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "receiver@") {
				t.Fatal("SMTP reply failure was lost or disclosed")
			}
		})
	}
}

func TestSMTPConfigurationCannotBeMutatedToDisableVerification(t *testing.T) {
	address, _, _ := smtpFixture(t, true, true)
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()}
	sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.invalid", TLSConfig: config, ImplicitTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	config.InsecureSkipVerify = true // deliberate mutation of the caller-owned configuration
	if err := sender.Send(context.Background(), Message{To: "receiver@example.invalid", Subject: "Verify", Text: "synthetic-token"}); err == nil {
		t.Fatal("caller mutation bypassed certificate verification")
	}
}

func TestSMTPBoundsAnUnterminatedServerGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		// A malicious dependency never terminates its response. The client must
		// stop at its byte budget without waiting for the network deadline.
		_, _ = io.WriteString(conn, "220-"+strings.Repeat("x", 256*1024))
		_, _ = io.Copy(io.Discard, conn)
	}()
	sender, err := NewSMTP(SMTPConfig{Address: listener.Addr().String(), From: "sender@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	err = sender.Send(ctx, Message{To: "receiver@example.invalid", Subject: "Verify", Text: "synthetic-token"})
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("unbounded SMTP server response")
	}
	<-done
}
