package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/mail"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
)

// emailOptions validates all mail dependencies before starting any worker.
func emailOptions(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) ([]service.Option, *mail.Worker, error) {
	var options []service.Option
	if cfg.EmailLegacyDeadline != nil {
		options = append(options, service.WithLegacyVerificationDeadline(*cfg.EmailLegacyDeadline))
	}
	if !cfg.MailEnabled {
		return options, nil, nil
	}
	key, err := privateFile(cfg.MailKeyFile, 128)
	if err != nil {
		return nil, nil, fmt.Errorf("AUTH_MAIL_KEY_FILE must be a private readable key file")
	}
	if len(key) != 32 {
		key, err = hex.DecodeString(strings.TrimSpace(string(key)))
		if err != nil {
			return nil, nil, errors.New("AUTH_MAIL_KEY_FILE must contain 32 raw bytes or 64 hex digits")
		}
	}
	box, err := mail.NewBox(key)
	clear(key)
	if err != nil {
		return nil, nil, errors.New("invalid AUTH_MAIL_KEY_FILE")
	}
	password := ""
	if cfg.SMTPPasswordFile != "" {
		data, err := privateFile(cfg.SMTPPasswordFile, 4096)
		if err != nil {
			return nil, nil, errors.New("AUTH_SMTP_PASSWORD_FILE must be private and readable")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		clear(data)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.SMTPCAFile != "" {
		file, err := os.Open(cfg.SMTPCAFile)
		if err != nil {
			return nil, nil, errors.New("cannot read AUTH_SMTP_CA_FILE")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 65537))
		_ = file.Close()
		if readErr != nil || len(data) > 65536 {
			return nil, nil, errors.New("invalid AUTH_SMTP_CA_FILE")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, nil, errors.New("invalid AUTH_SMTP_CA_FILE")
		}
		tlsConfig.RootCAs = roots
	}
	sender, err := mail.NewSMTP(mail.SMTPConfig{Address: cfg.SMTPAddress, From: cfg.SMTPFrom, Username: cfg.SMTPUsername, Password: password, ImplicitTLS: cfg.SMTPTLSMode == "tls", TLSConfig: tlsConfig})
	if err != nil {
		return nil, nil, errors.New("invalid SMTP delivery configuration")
	}
	store := repository.NewEmailRepository(pool)
	accounts, err := service.NewEmailAccounts(store, box, cfg.PublicURL)
	if err != nil {
		return nil, nil, errors.New("AUTH_PUBLIC_URL must be a trusted HTTPS origin (loopback HTTP is development-only)")
	}
	options = append(options, service.WithEmailAccounts(accounts))
	return options, mail.NewWorker(store, sender, box, logger), nil
}

func privateFile(name string, limit int64) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 {
		return nil, errors.New("private regular file required")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 {
		return nil, errors.New("private regular file required")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("private file size invalid")
	}
	return data, nil
}

func startMailWorker(ctx context.Context, worker *mail.Worker) func() {
	if worker == nil {
		return func() {}
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(workerCtx) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
	}
}
