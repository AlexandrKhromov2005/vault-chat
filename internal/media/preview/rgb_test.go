package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDecodeRGBIndependentPixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	w, h, rgb, err := DecodeRGB(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w != 2 || h != 1 {
		t.Fatalf("size %dx%d", w, h)
	}
	want := []byte{255, 0, 0, 0, 255, 0}
	if !bytes.Equal(rgb, want) {
		t.Fatalf("rgb %v, want %v", rgb, want)
	}
}
