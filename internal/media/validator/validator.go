// Package validator bounds untrusted media metadata and file content.
package validator

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // Register the supported image decoder.
	_ "image/png"  // Register the supported image decoder.
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxBytes is the fixed MVP limit per file: 10 MiB.
const MaxBytes = 10 << 20

var (
	// ErrInvalidFile rejects invalid names or empty/corrupt files.
	ErrInvalidFile = errors.New("media: invalid file")
	// ErrTooLarge rejects content above MaxBytes.
	ErrTooLarge = errors.New("media: file too large")
	// ErrUnsupported rejects content outside the allowlist.
	ErrUnsupported = errors.New("media: unsupported file type")
)

// Filename permits display names only; names never become storage paths.
func Filename(name string) error {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return ErrInvalidFile
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrInvalidFile
		}
	}
	return nil
}

// Content derives the type from bytes, never the client's Content-Type.
// PDF validation identifies its signature, not the absence of malware.
func Content(data []byte) (string, error) {
	if len(data) == 0 {
		return "", ErrInvalidFile
	}
	if len(data) > MaxBytes {
		return "", ErrTooLarge
	}
	typ := http.DetectContentType(data)
	switch typ {
	case "image/png", "image/jpeg":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return "", ErrInvalidFile
		}
	case "text/plain; charset=utf-8":
		if !utf8.Valid(data) {
			return "", ErrUnsupported
		}
	case "application/pdf":
	default:
		return "", ErrUnsupported
	}
	return typ, nil
}
