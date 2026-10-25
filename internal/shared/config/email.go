package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func (c *Config) loadEmail() error {
	if value := os.Getenv("AUTH_MAIL_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("AUTH_MAIL_ENABLED must be a boolean")
		}
		c.MailEnabled = enabled
	}
	if value := os.Getenv("AUTH_EMAIL_LEGACY_DEADLINE"); value != "" {
		deadline, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("AUTH_EMAIL_LEGACY_DEADLINE must be an RFC3339 timestamp")
		}
		c.EmailLegacyDeadline = &deadline
	}
	if !c.MailEnabled {
		return nil
	}
	var err error
	for name, target := range map[string]*string{
		"AUTH_PUBLIC_URL": &c.PublicURL, "AUTH_MAIL_KEY_FILE": &c.MailKeyFile,
		"AUTH_SMTP_ADDRESS": &c.SMTPAddress, "AUTH_SMTP_FROM": &c.SMTPFrom,
	} {
		*target, err = requiredEnv(name)
		if err != nil {
			return err
		}
	}
	c.SMTPUsername = os.Getenv("AUTH_SMTP_USERNAME")
	c.SMTPPasswordFile = os.Getenv("AUTH_SMTP_PASSWORD_FILE")
	if (c.SMTPUsername == "") != (c.SMTPPasswordFile == "") {
		return fmt.Errorf("AUTH_SMTP_USERNAME and AUTH_SMTP_PASSWORD_FILE must be configured together")
	}
	c.SMTPTLSMode = envOr("AUTH_SMTP_TLS_MODE", "starttls")
	if c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" {
		return fmt.Errorf("AUTH_SMTP_TLS_MODE must be starttls or tls")
	}
	c.SMTPCAFile = os.Getenv("AUTH_SMTP_CA_FILE")
	return nil
}
