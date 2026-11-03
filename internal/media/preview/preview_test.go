package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withExif inserts an APP1 Exif segment with one Orientation entry after SOI.
func withExif(data []byte, orientation uint16, order binary.ByteOrder) []byte {
	tiff := make([]byte, 26)
	if order == binary.BigEndian {
		copy(tiff, "MM\x00\x2a")
	} else {
		copy(tiff, "II\x2a\x00")
	}
	order.PutUint32(tiff[4:], 8)       // IFD0 offset
	order.PutUint16(tiff[8:], 1)       // one entry
	order.PutUint16(tiff[10:], 0x0112) // Orientation
	order.PutUint16(tiff[12:], 3)      // SHORT
	order.PutUint32(tiff[14:], 1)      // count
	order.PutUint16(tiff[18:], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	out := append([]byte{}, data[:2]...)
	out = append(out, segment...)
	out = append(out, payload...)
	return append(out, data[2:]...)
}

// pngHeader is a PNG that declares w×h pixels and ends after its header.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 17)
	copy(ihdr, "IHDR")
	binary.BigEndian.PutUint32(ihdr[4:], w)
	binary.BigEndian.PutUint32(ihdr[8:], h)
	ihdr[12], ihdr[13] = 8, 6 // 8-bit RGBA
	out := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != "jpeg" {
		t.Fatalf("preview is not a JPEG: %v %q", err, format)
	}
	return img
}

func mustMake(t *testing.T, data []byte) image.Image {
	t.Helper()
	out, err := Make(data)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	if len(out) > MaxOutput {
		t.Fatalf("preview of %d bytes exceeds %d", len(out), MaxOutput)
	}
	return decode(t, out)
}

func TestMakeBoundsTheLongerSide(t *testing.T) {
	got := mustMake(t, encodeJPEG(t, solid(1000, 500, color.NRGBA{R: 30, G: 120, B: 200, A: 255}))).Bounds()
	if got.Dx() != 320 || got.Dy() != 160 {
		t.Fatalf("1000×500 became %v, want 320×160", got.Size())
	}
}

func TestMakeKeepsSmallImages(t *testing.T) {
	got := mustMake(t, encodePNG(t, solid(100, 50, color.NRGBA{G: 255, A: 255}))).Bounds()
	if got.Dx() != 100 || got.Dy() != 50 {
		t.Fatalf("100×50 became %v; previews never enlarge", got.Size())
	}
}

func TestMakeKeepsColors(t *testing.T) {
	img := mustMake(t, encodePNG(t, solid(500, 300, color.NRGBA{R: 200, G: 40, B: 90, A: 255})))
	r, g, b := rgb(img.At(160, 96))
	if diff(r, 200) > 8 || diff(g, 40) > 8 || diff(b, 90) > 8 {
		t.Fatalf("color (%d,%d,%d), want about (200,40,90)", r, g, b)
	}
}

func TestMakeFlattensTransparencyOnWhite(t *testing.T) {
	img := mustMake(t, encodePNG(t, solid(64, 64, color.NRGBA{R: 255, A: 0})))
	if r, g, b := rgb(img.At(32, 32)); r < 245 || g < 245 || b < 245 {
		t.Fatalf("transparent pixels became (%d,%d,%d), want white", r, g, b)
	}
}

func TestMakeStripsMetadata(t *testing.T) {
	out, err := Make(withExif(encodeJPEG(t, solid(64, 48, color.NRGBA{R: 90, A: 255})), 1, binary.LittleEndian))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("Exif")) {
		t.Fatal("preview carries the original's Exif segment")
	}
}

func TestMakeIsDeterministic(t *testing.T) {
	data := encodeJPEG(t, solid(700, 400, color.NRGBA{R: 10, G: 200, B: 60, A: 255}))
	first, err1 := Make(data)
	second, err2 := Make(data)
	if err1 != nil || err2 != nil || !bytes.Equal(first, second) {
		t.Fatalf("two previews differ: %v %v", err1, err2)
	}
}

func TestMakeSkipsImagesAboveThePixelBudget(t *testing.T) {
	if _, err := Make(pngHeader(6000, 5000)); !errors.Is(err, ErrSkipped) {
		t.Fatalf("30-megapixel header: %v, want ErrSkipped", err)
	}
}

func TestMakeRejectsInvalidData(t *testing.T) {
	full := encodeJPEG(t, solid(300, 200, color.NRGBA{R: 1, A: 255}))
	for name, data := range map[string][]byte{
		"empty":     nil,
		"text":      []byte("not an image"),
		"truncated": full[:len(full)/2],
		"gif":       []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"no pixels": pngHeader(64, 64),
	} {
		if _, err := Make(data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func FuzzMake(f *testing.F) {
	f.Add(encodePNG(f, solid(12, 9, color.NRGBA{R: 9, A: 128})))
	f.Add(withExif(encodeJPEG(f, solid(16, 8, color.NRGBA{G: 9, A: 255})), 5, binary.BigEndian))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := Make(data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrSkipped) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
		if err != nil || format != "jpeg" || cfg.Width > MaxSide || cfg.Height > MaxSide || len(out) > MaxOutput {
			t.Fatalf("preview out of contract: %v %q %dx%d %d bytes", err, format, cfg.Width, cfg.Height, len(out))
		}
	})
}

func rgb(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

func diff(a uint8, b int) int {
	if d := int(a) - b; d >= 0 {
		return d
	}
	return b - int(a)
}
