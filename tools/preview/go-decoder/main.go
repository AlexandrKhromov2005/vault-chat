package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/AlexandrKhromov2005/vault-chat/internal/media/preview"
)

const maxInput = 10 << 20

func main() {
	os.Exit(run())
}

func run() int {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxInput+1))
	if err != nil {
		return fail(4, err)
	}
	if len(data) > maxInput {
		return fail(3, errors.New("input exceeds 10 MiB"))
	}
	w, h, rgb, err := preview.DecodeRGB(data)
	if errors.Is(err, preview.ErrSkipped) {
		return fail(3, err)
	}
	if err != nil {
		return fail(2, err)
	}
	if w < 1 || h < 1 || w > preview.MaxSide || h > preview.MaxSide || len(rgb) != w*h*3 {
		return fail(4, errors.New("invalid decoder result"))
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(w))
	binary.BigEndian.PutUint32(header[4:8], uint32(h))
	if err := writeAll(os.Stdout, header[:]); err != nil {
		return fail(4, err)
	}
	if err := writeAll(os.Stdout, rgb); err != nil {
		return fail(4, err)
	}
	return 0
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func fail(code int, err error) int {
	_, _ = fmt.Fprintf(os.Stderr, "preview: %v\n", err)
	return code
}
