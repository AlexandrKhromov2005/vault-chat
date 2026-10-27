package mail

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

var errDelivery = errors.New("mail delivery failed")

// SMTPConfig configures a TLS-only SMTP relay. Optional credentials require
// both username and password. TLSConfig is cloned and must verify certificates.
type SMTPConfig struct {
	Address, From, Username, Password string
	ImplicitTLS                       bool
	TLSConfig                         *tls.Config
}

// SMTP delivers one-recipient notifications through a configured TLS relay.
type SMTP struct {
	address, host, from, fromHeader, username, password string
	implicitTLS                                         bool
	tlsConfig                                           *tls.Config
}

// NewSMTP rejects insecure TLS, invalid addresses and header injection.
func NewSMTP(config SMTPConfig) (*SMTP, error) {
	host, port, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" || len(config.Address) > 512 || hasControl(config.Address) {
		return nil, errDelivery
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, errDelivery
	}
	from, err := mailbox(config.From, true)
	if err != nil {
		return nil, errDelivery
	}
	if (config.Username == "") != (config.Password == "") ||
		len(config.Username) > 1024 || len(config.Password) > 4096 ||
		strings.ContainsRune(config.Username, 0) || strings.ContainsRune(config.Password, 0) {
		return nil, errDelivery
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
		if tlsConfig.InsecureSkipVerify ||
			(tlsConfig.MinVersion != 0 && tlsConfig.MinVersion < tls.VersionTLS12) ||
			(tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS12) {
			return nil, errDelivery
		}
		if tlsConfig.RootCAs != nil {
			tlsConfig.RootCAs = tlsConfig.RootCAs.Clone()
		}
	}
	if tlsConfig.MinVersion == 0 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = host
	}
	return &SMTP{address: config.Address, host: host, from: from.Address, fromHeader: from.String(),
		username: config.Username, password: config.Password, implicitTLS: config.ImplicitTLS, tlsConfig: tlsConfig}, nil
}

// Send verifies TLS before authentication or envelope transmission. Its total
// network deadline is ten seconds, and cancellation closes the connection.
// Errors deliberately contain no SMTP replies, addresses, credentials or tokens.
func (s *SMTP) Send(ctx context.Context, message Message) error {
	if s == nil {
		return errDelivery
	}
	if _, err := encodeMessage(message); err != nil {
		return errDelivery
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := new(net.Dialer).DialContext(ctx, "tcp", s.address)
	if err != nil {
		return errDelivery
	}
	defer func() { _ = raw.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return errDelivery
	}
	// Bound all server input, including greetings, TLS handshake and responses.
	// net/smtp otherwise accepts arbitrarily large multiline server replies.
	var connection net.Conn = &replyBudget{Conn: raw, remaining: 256 * 1024}
	if s.implicitTLS {
		secure := tls.Client(connection, s.tlsConfig.Clone())
		if err := secure.HandshakeContext(ctx); err != nil {
			return errDelivery
		}
		connection = secure
	}
	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		return errDelivery
	}
	defer func() { _ = client.Close() }()
	if !s.implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errDelivery
		}
		if err := client.StartTLS(s.tlsConfig.Clone()); err != nil {
			return errDelivery
		}
	}
	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return errDelivery
		}
	}
	if err := client.Mail(s.from); err != nil {
		return errDelivery
	}
	if err := client.Rcpt(message.To); err != nil {
		return errDelivery
	}
	writer, err := client.Data()
	if err != nil {
		return errDelivery
	}
	if _, err := io.WriteString(writer, s.wireMessage(message)); err != nil {
		return errDelivery
	}
	if err := writer.Close(); err != nil {
		return errDelivery
	}
	// DATA's successful final reply means accepted. A later QUIT failure must
	// not turn an accepted notification into a duplicate retry.
	_ = client.Quit()
	return nil
}

func (s *SMTP) wireMessage(message Message) string {
	var wire strings.Builder
	wire.WriteString("From: " + s.fromHeader + "\r\nTo: " + message.To + "\r\n")
	wire.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", message.Subject) + "\r\n")
	wire.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	wire.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n")
	wire.WriteString("Content-Transfer-Encoding: base64\r\nAuto-Submitted: auto-generated\r\n\r\n")
	body := base64.StdEncoding.EncodeToString([]byte(message.Text))
	for len(body) > 76 {
		wire.WriteString(body[:76] + "\r\n")
		body = body[76:]
	}
	wire.WriteString(body + "\r\n")
	return wire.String()
}

type replyBudget struct {
	net.Conn
	remaining int
}

func (b *replyBudget) Read(data []byte) (int, error) {
	if b.remaining == 0 {
		return 0, errDelivery
	}
	if len(data) > b.remaining {
		data = data[:b.remaining]
	}
	n, err := b.Conn.Read(data)
	b.remaining -= n
	return n, err
}
