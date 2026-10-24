// Package validator bounds untrusted media metadata and file content.
package validator

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	_ "image/jpeg" // Register the supported image decoder.
	_ "image/png"  // Register the supported image decoder.
	"net/http"
	"path"
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
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || name == "." || strings.HasSuffix(name, ".") || strings.ContainsAny(name, `/\:`) {
		return ErrInvalidFile
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ErrInvalidFile
		}
	}
	return nil
}

// DownloadFilename binds the extension to the detected type, including for
// files stored before this rule existed. Unsafe legacy names get a fallback.
func DownloadFilename(name, typ string) string {
	ext := ".txt"
	switch typ {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "application/pdf":
		ext = ".pdf"
	}
	if Filename(name) == nil {
		current := strings.ToLower(path.Ext(name))
		if current == ext || (typ == "image/jpeg" && current == ".jpeg") {
			return name
		}
		if len(name)+len(ext) <= 255 {
			return name + ext
		}
	}
	return "attachment" + ext
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
		// The standard sniffer labels SVG without an XML declaration as text.
		// Reject active XML roots even when comments precede the root element.
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			if root, ok := token.(xml.StartElement); ok {
				switch strings.ToLower(root.Name.Local) {
				case "svg", "html", "script":
					return "", ErrUnsupported
				}
				break
			}
		}
		if !utf8.Valid(data) {
			return "", ErrUnsupported
		}
	case "application/pdf":
	default:
		return "", ErrUnsupported
	}
	return typ, nil
}
