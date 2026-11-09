package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"time"
)

const (
	// MaxInput is the largest original accepted by a decoder worker.
	MaxInput          = 10 << 20
	maxWorkerResponse = 8 + MaxSide*MaxSide*3
	maxWorkerStderr   = 16 << 10
	defaultTimeout    = 5 * time.Second
)

var (
	// ErrWorker marks a decoder crash, timeout or invalid process response.
	ErrWorker = errors.New("preview: decoder worker failed")
)

// Result is the bounded RGB response of a decoder worker.
type Result struct {
	Width  int
	Height int
	RGB    []byte
}

// Worker launches a fresh decoder process for every image.
type Worker struct {
	Path    string
	Args    []string
	Timeout time.Duration
}

func expectedSize(data []byte) (int, int, error) {
	if len(data) > MaxInput {
		return 0, 0, ErrSkipped
	}
	cfg, format, err := inspect(data)
	if err != nil {
		return 0, 0, err
	}
	w, h := fit(cfg.Width, cfg.Height)
	if format == "jpeg" && orientation(data) >= 5 {
		w, h = h, w
	}
	return w, h, nil
}

func inspect(data []byte) (image.Config, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width <= 0 || cfg.Height <= 0 {
		return image.Config{}, "", ErrInvalid
	}
	need, ok := decodedBytes(data, format, cfg.Width, cfg.Height)
	if !ok {
		return image.Config{}, "", ErrInvalid
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels || need > MaxDecoded {
		return image.Config{}, "", ErrSkipped
	}
	return cfg, format, nil
}

func parseWorkerResponse(data []byte, width, height int) (Result, error) {
	if width < 1 || height < 1 || width > MaxSide || height > MaxSide || len(data) != 8+width*height*3 {
		return Result{}, fmt.Errorf("%w: response length %d for %dx%d", ErrWorker, len(data), width, height)
	}
	w := int(binary.BigEndian.Uint32(data))
	h := int(binary.BigEndian.Uint32(data[4:]))
	if w != width || h != height {
		return Result{}, fmt.Errorf("%w: response dimensions %dx%d, want %dx%d", ErrWorker, w, h, width, height)
	}
	return Result{Width: w, Height: h, RGB: data[8:]}, nil
}
