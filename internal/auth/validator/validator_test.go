package validator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{"simple", "user@example.com", false},
		{"shortest", "a@b.co", false},
		{"plus tag", "test.user+tag@domain.org", false},
		{"underscore subdomain", "user_name@sub.domain.io", false},
		{"digits", "user123@example42.com", false},
		{"empty", "", true},
		{"no at", "userexample.com", true},
		{"two at", "two@@example.com", true},
		{"space inside", "space in@addr.com", true},
		{"leading space", " user@example.com", true},
		{"missing local", "@example.com", true},
		{"missing domain", "user@", true},
		{"domain without tld", "user@localhost", true},
		{"too long", strings.Repeat("a", 245) + "@example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateEmail(tt.email)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"min length", "abc", false},
		{"max length", "a2345678901234567890123456789012", false},
		{"underscore and dash", "a_b-c1", false},
		{"mixed case", "User_Name", false},
		{"empty", "", true},
		{"too short", "ab", true},
		{"too long", "a" + strings.Repeat("1", 32), true},
		{"starts with digit", "1abc", true},
		{"starts with dash", "-abc", true},
		{"starts with underscore", "_abc", true},
		{"space inside", "a b", true},
		{"special char", "a!b", true},
		{"cyrillic", "пользователь", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateUsername(tt.username)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"letters and digits", "password1", false},
		{"min length", "abcd1234", false},
		{"with symbols", "p@ssw0rd!#$", false},
		{"max length", strings.Repeat("a1", 64), false},
		{"empty", "", true},
		{"too short", "abcd123", true},
		{"letters only", "onlyletters", true},
		{"digits only", "12345678", true},
		{"too long", strings.Repeat("a1", 64) + "x", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidatePassword(tt.password)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateUserID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"canonical uuid", "3f2b8c1e-9d4a-4e6b-8f7c-2a1d0e9b8c7d", false},
		{"uppercase uuid", "3F2B8C1E-9D4A-4E6B-8F7C-2A1D0E9B8C7D", false},
		{"empty", "", true},
		{"not a uuid", "user-1", true},
		{"missing dashes", "3f2b8c1e9d4a4e6b8f7c2a1d0e9b8c7d", true},
		{"braced", "{3f2b8c1e-9d4a-4e6b-8f7c-2a1d0e9b8c7d}", true},
		{"urn form", "urn:uuid:3f2b8c1e-9d4a-4e6b-8f7c-2a1d0e9b8c7d", true},
		{"non-hex digit", "3f2b8c1e-9d4a-4e6b-8f7c-2a1d0e9b8c7z", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateUserID(tt.id)
			if tt.wantErr {
				require.ErrorIs(t, err, validator.ErrInvalidUserID)
				return
			}
			require.NoError(t, err)
		})
	}
}
