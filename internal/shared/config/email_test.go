package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/config"
)

func TestEmailEnablementNeedsDeliverySettings(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUTH_MAIL_ENABLED", "true")
	_, err := config.Load()
	require.Error(t, err, "enabling email without delivery settings must fail startup")
}

func TestEmailSettingsAndLegacyDeadline(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUTH_MAIL_ENABLED", "true")
	for k, v := range map[string]string{"AUTH_PUBLIC_URL": "https://chat.example.invalid", "AUTH_MAIL_KEY_FILE": "/run/secrets/mail/outbox.key", "AUTH_SMTP_ADDRESS": "smtp.example.invalid:587", "AUTH_SMTP_FROM": "accounts@example.invalid", "AUTH_SMTP_USERNAME": "fixture", "AUTH_SMTP_PASSWORD_FILE": "/run/secrets/mail/smtp_password", "AUTH_EMAIL_LEGACY_DEADLINE": "2030-01-01T00:00:00Z"} {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	require.True(t, cfg.MailEnabled)
	require.Equal(t, "starttls", cfg.SMTPTLSMode)
	require.NotNil(t, cfg.EmailLegacyDeadline)
	for key, value := range map[string]string{"AUTH_MAIL_ENABLED": "typo", "AUTH_SMTP_TLS_MODE": "none", "AUTH_EMAIL_LEGACY_DEADLINE": "not-a-date"} {
		t.Run(key, func(t *testing.T) { t.Setenv(key, value); _, err := config.Load(); require.ErrorContains(t, err, key) })
	}
	t.Run("credentials pair", func(t *testing.T) {
		t.Setenv("AUTH_SMTP_PASSWORD_FILE", "")
		_, err := config.Load()
		require.ErrorContains(t, err, "AUTH_SMTP_PASSWORD_FILE")
	})
	t.Run("disabled delivery retains cutoff", func(t *testing.T) {
		t.Setenv("AUTH_MAIL_ENABLED", "false")
		cfg, err := config.Load()
		require.NoError(t, err)
		require.False(t, cfg.MailEnabled)
		require.NotNil(t, cfg.EmailLegacyDeadline)
	})
}
