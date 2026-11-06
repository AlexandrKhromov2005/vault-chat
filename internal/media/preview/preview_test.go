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

// pngHeader is an RGBA PNG that declares w×h pixels and ends after its header.
func pngHeader(w, h uint32, depth, interlace byte) []byte {
	ihdr := make([]byte, 17)
	copy(ihdr, "IHDR")
	binary.BigEndian.PutUint32(ihdr[4:], w)
	binary.BigEndian.PutUint32(ihdr[8:], h)
	ihdr[12], ihdr[13], ihdr[16] = depth, 6, interlace
	out := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

// jpegHeader is a JPEG that declares w×h pixels in a frame with the given
// marker and sampling factors (0x11 or 0x22) and ends after its header.
func jpegHeader(w, h uint16, marker byte, sampling ...byte) []byte {
	sof := []byte{0xff, marker, 0, byte(8 + 3*len(sampling)), 8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), byte(len(sampling))}
	for i, s := range sampling {
		sof = append(sof, byte(i+1), s, 0)
	}
	return append(append([]byte{0xff, 0xd8}, sof...), 0xff, 0xda, 0, 2)
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

func TestMakeAppliesOrientation(t *testing.T) {
	// Left half red, right half blue; orientation 6 rotates 90° clockwise,
	// so red ends up on top.
	src := solid(40, 20, color.NRGBA{B: 255, A: 255})
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			src.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	img := mustMake(t, withExif(encodeJPEG(t, src), 6, binary.BigEndian))
	if got := img.Bounds().Size(); got != image.Pt(20, 40) {
		t.Fatalf("rotated size %v, want 20×40", got)
	}
	if r, _, b := rgb(img.At(10, 5)); r < 200 || b > 60 {
		t.Fatalf("top is (%d,_,%d), want red", r, b)
	}
	if r, _, b := rgb(img.At(10, 35)); b < 200 || r > 60 {
		t.Fatalf("bottom is (%d,_,%d), want blue", r, b)
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

// A preview pixel covers 1.5 source pixels of the pattern 0, 255, 0: one
// whole and half of the next, so every pixel is (0 + 127.5) / 1.5 = 85.
func TestShrinkSplitsSourcePixels(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 480, 1))
	for x := range src.Pix {
		if x%3 == 1 {
			src.Pix[x] = 255
		}
	}
	dst := shrink(src)
	for x := 0; x < dst.Bounds().Dx(); x++ {
		if v := dst.Pix[x*4]; diff(v, 85) > 1 {
			t.Fatalf("preview pixel %d = %d, want the area average 85", x, v)
		}
	}
}

// shrink matches a direct area average for ratios that are not integers.
func TestShrinkAveragesByArea(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 777, 451))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 7919 >> 3)
	}
	dst := shrink(src)
	dw, dh := dst.Bounds().Dx(), dst.Bounds().Dy()
	// overlap is the length of [a/n, (a+1)/n) ∩ [b/m, (b+1)/m), scaled by n·m.
	overlap := func(a, n, b, m int) float64 {
		return float64(max(0, min((a+1)*m, (b+1)*n)-max(a*m, b*n)))
	}
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var sum, area float64
			for y := dy * 451 / dh; y <= min(450, (dy+1)*451/dh); y++ {
				for x := dx * 777 / dw; x <= min(776, (dx+1)*777/dw); x++ {
					w := overlap(x, 777, dx, dw) * overlap(y, 451, dy, dh)
					sum += w * float64(src.Pix[y*777+x])
					area += w
				}
			}
			if got := dst.Pix[dy*dst.Stride+dx*4]; diff(got, int(sum/area+0.5)) > 1 {
				t.Fatalf("pixel (%d,%d) = %d, want %.1f", dx, dy, got, sum/area)
			}
		}
	}
}

func TestMakeSkipsImagesAboveThePixelBudget(t *testing.T) {
	if _, err := Make(pngHeader(6000, 5000, 8, 0)); !errors.Is(err, ErrSkipped) {
		t.Fatalf("30-megapixel header: %v, want ErrSkipped", err)
	}
}

// The memory budget depends on the encoding, not only on the pixel count.
// Headers within it reach the decoder, which rejects the missing pixels.
func TestMakeSkipsEncodingsAboveTheMemoryBudget(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
		skip bool
	}{
		{"PNG 8-bit 24 MP", pngHeader(6000, 4000, 8, 0), false},
		{"PNG 16-bit 24 MP", pngHeader(6000, 4000, 16, 0), true},
		{"PNG 16-bit 12 MP", pngHeader(4000, 3000, 16, 0), false},
		{"PNG interlaced 24 MP", pngHeader(6000, 4000, 8, 1), true},
		{"PNG interlaced 12 MP", pngHeader(4000, 3000, 8, 1), false},
		{"PNG 24 000 000×1", pngHeader(24_000_000, 1, 8, 0), true},
		{"JPEG baseline 4:4:4 24 MP", jpegHeader(6000, 4000, 0xc0, 0x11, 0x11, 0x11), false},
		{"JPEG progressive 4:4:4 24 MP", jpegHeader(6000, 4000, 0xc2, 0x11, 0x11, 0x11), true},
		{"JPEG progressive 4:2:0 24 MP", jpegHeader(6000, 4000, 0xc2, 0x22, 0x11, 0x11), true},
		{"JPEG progressive 4:2:0 9 MP", jpegHeader(3000, 3000, 0xc2, 0x22, 0x11, 0x11), false},
		{"JPEG CMYK 24 MP", jpegHeader(6000, 4000, 0xc0, 0x11, 0x11, 0x11, 0x11), true},
		{"JPEG CMYK 9 MP", jpegHeader(3000, 3000, 0xc0, 0x11, 0x11, 0x11, 0x11), false},
	} {
		want := ErrInvalid
		if c.skip {
			want = ErrSkipped
		}
		if _, err := Make(c.data); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", c.name, err, want)
		}
	}
}

// The allocation-free row readers agree with compositing At on white.
func TestRowReadersMatchAt(t *testing.T) {
	r := image.Rect(0, 0, 61, 3)
	gray, rgba, nrgba, cmyk := image.NewGray16(r), image.NewRGBA64(r), image.NewNRGBA64(r), image.NewCMYK(r)
	for y := 0; y < 3; y++ {
		for x := 0; x < 61; x++ {
			v := func(k int) uint16 { return uint16((x*7919 + y*104729 + k*15485863) % 65536) }
			a := v(3)
			gray.SetGray16(x, y, color.Gray16{Y: v(0)})
			rgba.SetRGBA64(x, y, color.RGBA64{R: v(0) % (a + 1), G: v(1) % (a + 1), B: v(2) % (a + 1), A: a})
			nrgba.SetNRGBA64(x, y, color.NRGBA64{R: v(0), G: v(1), B: v(2), A: a})
			cmyk.SetCMYK(x, y, color.CMYK{C: uint8(v(0)), M: uint8(v(1)), Y: uint8(v(2)), K: uint8(a)})
		}
	}
	for _, img := range []image.Image{gray, rgba, nrgba, cmyk} {
		row := make([]uint8, 61*3)
		for y := 0; y < 3; y++ {
			rowReader(img)(y, row)
			for x := 0; x < 61; x++ {
				want := onWhite(img.At(x, y))
				for c := 0; c < 3; c++ {
					if diff(row[x*3+c], int(want[c])) > 1 {
						t.Fatalf("%T (%d,%d): %v, want %v", img, x, y, row[x*3:x*3+3], want)
					}
				}
			}
		}
	}
}

func TestMakeRejectsInvalidData(t *testing.T) {
	full := encodeJPEG(t, solid(300, 200, color.NRGBA{R: 1, A: 255}))
	for name, data := range map[string][]byte{
		"empty":     nil,
		"text":      []byte("not an image"),
		"truncated": full[:len(full)/2],
		"gif":       []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"no pixels": pngHeader(64, 64, 8, 0),
	} {
		if _, err := Make(data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestOrientation(t *testing.T) {
	plain := encodeJPEG(t, solid(8, 8, color.NRGBA{A: 255}))
	cases := map[string]struct {
		data []byte
		want int
	}{
		"big endian":    {withExif(plain, 6, binary.BigEndian), 6},
		"little endian": {withExif(plain, 8, binary.LittleEndian), 8},
		"no exif":       {plain, 1},
		"out of range":  {withExif(plain, 9, binary.BigEndian), 1},
		"truncated":     {withExif(plain, 3, binary.BigEndian)[:30], 1},
		"not a jpeg":    {[]byte("plain text"), 1},
	}
	for name, c := range cases {
		if got := orientation(c.data); got != c.want {
			t.Errorf("%s: orientation %d, want %d", name, got, c.want)
		}
	}
}

func FuzzOrientation(f *testing.F) {
	plain := encodeJPEG(f, solid(8, 8, color.NRGBA{A: 255}))
	f.Add(plain)
	f.Add(withExif(plain, 6, binary.BigEndian))
	f.Add(withExif(plain, 3, binary.LittleEndian))
	f.Fuzz(func(t *testing.T, data []byte) {
		if got := orientation(data); got < 1 || got > 8 {
			t.Fatalf("orientation %d outside 1..8", got)
		}
	})
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
