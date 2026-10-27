package main

import (
	"bytes"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
)

func TestEmailDeliveryStartupAndPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	keyfile := filepath.Join(dir, "key")
	key := bytes.Repeat([]byte{7}, 32)
	require.NoError(t, os.WriteFile(keyfile, key, 0600))
	cfg := &config.Config{MailEnabled: true, PublicURL: "https://chat.example.invalid", MailKeyFile: keyfile, SMTPAddress: "smtp.example.invalid:587", SMTPFrom: "accounts@example.invalid", SMTPTLSMode: "starttls"}
	options, worker, err := emailOptions(cfg, nil, slog.Default())
	require.NoError(t, err)
	require.NotEmpty(t, options)
	require.NotNil(t, worker)
	// Constructor validation must not dial a real SMTP service.
	require.NoError(t, os.WriteFile(keyfile, []byte(hex.EncodeToString(key)+"\n"), 0600))
	_, _, err = emailOptions(cfg, nil, slog.Default())
	require.NoError(t, err)
	require.NoError(t, os.Chmod(keyfile, 0644))
	_, _, err = emailOptions(cfg, nil, slog.Default())
	require.Error(t, err)
	require.NotContains(t, err.Error(), hex.EncodeToString(key))
	require.NoError(t, os.Chmod(keyfile, 0600))
	require.NoError(t, os.WriteFile(keyfile, []byte("short"), 0600))
	_, _, err = emailOptions(cfg, nil, slog.Default())
	require.Error(t, err)
	require.NoError(t, os.WriteFile(keyfile, key, 0600))
	cfg.PublicURL = "http://remote.example.invalid"
	_, _, err = emailOptions(cfg, nil, slog.Default())
	require.Error(t, err)
	cfg.PublicURL = "https://chat.example.invalid"
	cfg.SMTPFrom = "bad\r\nBCC: victim@example.invalid"
	_, _, err = emailOptions(cfg, nil, slog.Default())
	require.Error(t, err)
	cfg.SMTPFrom = "accounts@example.invalid"
	cfg.SMTPCAFile = filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(cfg.SMTPCAFile, []byte("not-a-certificate"), 0600))
	_, _, err = emailOptions(cfg, nil, slog.Default())
	require.Error(t, err)
}

func TestPrivateFileBoundsAndDisabledMail(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "secret")
	for _, data := range [][]byte{nil, bytes.Repeat([]byte{1}, 129)} {
		require.NoError(t, os.WriteFile(name, data, 0600))
		_, err := privateFile(name, 128)
		require.Error(t, err)
	}
	_, err := privateFile(dir, 128)
	require.Error(t, err)
	_, err = privateFile(filepath.Join(dir, "missing"), 128)
	require.Error(t, err)
	options, worker, err := emailOptions(&config.Config{}, nil, slog.Default())
	require.NoError(t, err)
	require.Empty(t, options)
	require.Nil(t, worker)
}
