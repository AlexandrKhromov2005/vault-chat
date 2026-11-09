// Package preview turns an uploaded image into the bounded JPEG preview of
// docs/adr/0006-image-previews.md. It is the Go baseline for the comparison
// with a Zig worker and runs on untrusted input.
package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

const (
	// MaxSide bounds the longer side of a preview in pixels.
	MaxSide = 320
	// MaxPixels bounds the pixel count of an image that gets a preview;
	// MaxDecoded bounds the memory its encoding takes to decode.
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

// Make returns the JPEG preview of a PNG or JPEG image. The pixel and memory
// budgets are checked on the header, before anything is decoded.
func Make(data []byte) ([]byte, error) {
	small, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: Quality}); err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalid, err)
	}
	if buf.Len() > MaxOutput {
		return nil, fmt.Errorf("%w: preview of %d bytes", ErrInvalid, buf.Len())
	}
	return buf.Bytes(), nil
}

// DecodeRGB returns the preview dimensions and packed RGB pixels for the
// process-worker comparison. It does not encode JPEG or preserve metadata.
func DecodeRGB(data []byte) (int, int, []byte, error) {
	img, err := decodeImage(data)
	if err != nil {
		return 0, 0, nil, err
	}
	b := img.Bounds()
	rgb := make([]byte, b.Dx()*b.Dy()*3)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			i := y*img.Stride + x*4
			j := (y*b.Dx() + x) * 3
			copy(rgb[j:j+3], img.Pix[i:i+3])
		}
	}
	return b.Dx(), b.Dy(), rgb, nil
}

func decodeImage(data []byte) (*image.RGBA, error) {
	_, format, err := inspect(data)
	if err != nil {
		return nil, err
	}
	var img image.Image
	if format == "png" {
		img, err = png.Decode(bytes.NewReader(data))
	} else {
		img, err = jpeg.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, ErrInvalid
	}
	small := shrink(img)
	if format == "jpeg" {
		small = orient(small, orientation(data))
	}
	return small, nil
}

// fit returns the preview size: the longer side becomes MaxSide, and images
// that already fit keep their size.
func fit(w, h int) (int, int) {
	if w <= MaxSide && h <= MaxSide {
		return w, h
	}
	if w >= h {
		return MaxSide, max(1, (h*MaxSide+w/2)/w)
	}
	return max(1, (w*MaxSide+h/2)/h), MaxSide
}

// shrink resamples by area averaging: every source pixel contributes to the
// preview pixels it overlaps in proportion to the overlap, in exact integer
// weights. The source is read once row by row. Transparency is flattened on
// white.
func shrink(img image.Image) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := fit(sw, sh)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	read := rowReader(img)
	row := make([]uint8, sw*3)
	line := make([]uint64, dw*3)  // one source row, resampled horizontally
	acc := make([]uint64, 2*dw*3) // preview rows cur and cur+1
	area := uint64(sw) * uint64(sh)
	cur := 0
	flush := func() {
		out := dst.Pix[cur*dst.Stride:]
		for dx := 0; dx < dw; dx++ {
			for c := 0; c < 3; c++ {
				out[dx*4+c] = uint8((acc[dx*3+c] + area/2) / area)
			}
			out[dx*4+3] = 255
		}
		copy(acc, acc[dw*3:])
		clear(acc[dw*3:])
	}
	for y := 0; y < sh; y++ {
		dy, v := split(y, sh, dh)
		if dy != cur {
			flush()
			cur = dy
		}
		read(b.Min.Y+y, row)
		clear(line)
		for x := 0; x < sw; x++ {
			dx, w := split(x, sw, dw)
			for c := 0; c < 3; c++ {
				s := uint64(row[x*3+c])
				line[dx*3+c] += s * w
				if w < uint64(dw) {
					line[dx*3+3+c] += s * (uint64(dw) - w)
				}
			}
		}
		for i, s := range line {
			acc[i] += s * v
			acc[dw*3+i] += s * (uint64(dh) - v)
		}
	}
	flush()
	return dst
}

// split maps source pixel i of n onto m ≤ n preview pixels. Measured in
// units of 1/(n·m), the pixel puts weight w into preview pixel j and the
// rest, m-w, into j+1. The weights of each preview pixel add up to n.
func split(i, n, m int) (int, uint64) {
	j := i * m / n
	return j, uint64(min((i+1)*m, (j+1)*n) - i*m)
}

// rowReader returns a function that writes one source row as RGB on white.
// Every image type the decoders return has a path that does not allocate:
// At allocates per pixel, and that garbage would double the heap.
func rowReader(img image.Image) func(y int, row []uint8) {
	b := img.Bounds()
	switch m := img.(type) {
	case *image.Gray16:
		return func(y int, row []uint8) {
			pix := m.Pix[m.PixOffset(b.Min.X, y):]
			for x := 0; x < b.Dx(); x++ {
				row[x*3], row[x*3+1], row[x*3+2] = pix[x*2], pix[x*2], pix[x*2]
			}
		}
	case *image.RGBA64:
		return rows64(func(y int) []uint8 { return m.Pix[m.PixOffset(b.Min.X, y):] }, b.Dx(), false)
	case *image.NRGBA64:
		return rows64(func(y int) []uint8 { return m.Pix[m.PixOffset(b.Min.X, y):] }, b.Dx(), true)
	case *image.CMYK:
		return func(y int, row []uint8) {
			pix := m.Pix[m.PixOffset(b.Min.X, y):]
			for x := 0; x < b.Dx(); x++ {
				p := pix[x*4 : x*4+4]
				row[x*3], row[x*3+1], row[x*3+2] = color.CMYKToRGB(p[0], p[1], p[2], p[3])
			}
		}
	case *image.YCbCr:
		return func(y int, row []uint8) {
			for x := b.Min.X; x < b.Max.X; x++ {
				ci := m.COffset(x, y)
				r, g, bl := color.YCbCrToRGB(m.Y[m.YOffset(x, y)], m.Cb[ci], m.Cr[ci])
				i := (x - b.Min.X) * 3
				row[i], row[i+1], row[i+2] = r, g, bl
			}
		}
	case *image.Gray:
		return func(y int, row []uint8) {
			pix := m.Pix[m.PixOffset(b.Min.X, y):]
			for x := 0; x < b.Dx(); x++ {
				row[x*3], row[x*3+1], row[x*3+2] = pix[x], pix[x], pix[x]
			}
		}
	case *image.NRGBA:
		return func(y int, row []uint8) {
			pix := m.Pix[m.PixOffset(b.Min.X, y):]
			for x := 0; x < b.Dx(); x++ {
				a := uint32(pix[x*4+3])
				for c := 0; c < 3; c++ {
					row[x*3+c] = uint8((uint32(pix[x*4+c])*a + 255*(255-a) + 127) / 255)
				}
			}
		}
	case *image.RGBA:
		return func(y int, row []uint8) {
			pix := m.Pix[m.PixOffset(b.Min.X, y):]
			for x := 0; x < b.Dx(); x++ {
				white := 255 - uint16(pix[x*4+3])
				for c := 0; c < 3; c++ {
					// Saturate: malformed premultiplied data may exceed alpha.
					row[x*3+c] = uint8(min(255, uint16(pix[x*4+c])+white))
				}
			}
		}
	case *image.Paletted:
		palette := make([][3]uint8, len(m.Palette))
		for i, c := range m.Palette {
			palette[i] = onWhite(c)
		}
		return func(y int, row []uint8) {
			pix := m.Pix[m.PixOffset(b.Min.X, y):]
			for x := 0; x < b.Dx(); x++ {
				c := [3]uint8{255, 255, 255}
				if int(pix[x]) < len(palette) {
					c = palette[pix[x]]
				}
				copy(row[x*3:], c[:])
			}
		}
	default:
		return func(y int, row []uint8) {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := onWhite(img.At(x, y))
				copy(row[(x-b.Min.X)*3:], c[:])
			}
		}
	}
}

// rows64 reads 16-bit RGBA rows, premultiplied unless straight, on white.
func rows64(start func(y int) []uint8, w int, straight bool) func(y int, row []uint8) {
	return func(y int, row []uint8) {
		pix := start(y)
		for x := 0; x < w; x++ {
			a := uint32(binary.BigEndian.Uint16(pix[x*8+6:]))
			for c := 0; c < 3; c++ {
				v := uint32(binary.BigEndian.Uint16(pix[x*8+c*2:]))
				if straight {
					v = v * a / 0xffff
				}
				// Saturate: malformed premultiplied data may exceed alpha.
				row[x*3+c] = uint8(min(0xffff, v+0xffff-a) >> 8)
			}
		}
	}
}

// onWhite composites a color over white.
func onWhite(c color.Color) [3]uint8 {
	r, g, b, a := c.RGBA() // premultiplied, 16 bits
	white := 0xffff - a
	return [3]uint8{uint8((r + white) >> 8), uint8((g + white) >> 8), uint8((b + white) >> 8)}
}

// orient applies an EXIF orientation (1–8) to the preview.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			copy(dst.Pix[y*dst.Stride+x*4:y*dst.Stride+x*4+4], src.Pix[sy*src.Stride+sx*4:])
		}
	}
	return dst
}
