package main

import (
	"image"
	"image/jpeg"
	"math/rand/v2"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: preview-fixture OUTPUT")
	}
	const w, h = 6000, 4000
	rng := rand.New(rand.NewPCG(24, 7))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := rng.IntN(24)
			i := y*img.Stride + x*4
			img.Pix[i] = uint8(min(255, x*255/w+n))
			img.Pix[i+1] = uint8(min(255, y*255/h+n))
			img.Pix[i+2] = uint8((x+y)*127/(w+h) + n)
			img.Pix[i+3] = 255
		}
	}
	file, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		panic(err)
	}
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
}
