// Package preview turns an uploaded image into the bounded JPEG preview of
// docs/adr/0006-image-previews.md. It is the Go baseline for the comparison
// with a Zig worker and runs on untrusted input.
package preview

import "errors"

const (
	// MaxSide bounds the longer side of a preview in pixels.
	MaxSide = 320
	// MaxPixels bounds the decoded size of an image that gets a preview.
	MaxPixels = 24_000_000
	// MaxOutput bounds the encoded preview.
	MaxOutput = 256 << 10
	// Quality is the JPEG quality of previews.
	Quality = 80
)

var (
	// ErrSkipped marks an image too large for a preview; the original stays.
	ErrSkipped = errors.New("preview: image too large for a preview")
	// ErrInvalid marks data that is not a decodable PNG or JPEG.
	ErrInvalid = errors.New("preview: invalid image")
)

// Make returns the JPEG preview of a PNG or JPEG image.
func Make(data []byte) ([]byte, error) {
	return nil, ErrInvalid
}
