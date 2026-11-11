//go:build linux

package preview

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type processResult struct {
	width, height int
	rgb           []byte
	elapsed       time.Duration
}

func workers(t *testing.T) (string, string) {
	t.Helper()
	goWorker, zigWorker := os.Getenv("PREVIEW_GO_WORKER"), os.Getenv("PREVIEW_ZIG_WORKER")
	if goWorker == "" || zigWorker == "" {
		t.Skip("set PREVIEW_GO_WORKER and PREVIEW_ZIG_WORKER")
	}
	return goWorker, zigWorker
}

func runProcess(t *testing.T, worker string, data []byte) (processResult, int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, worker)
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	result := processResult{elapsed: time.Since(start)}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("%s: %v", worker, err)
		}
		code = exit.ExitCode()
	}
	if code != 0 {
		return result, code, stderr.String()
	}
	data = stdout.Bytes()
	if len(data) < 8 {
		t.Fatalf("%s: short response %d bytes", worker, len(data))
	}
	result.width = int(binary.BigEndian.Uint32(data[0:4]))
	result.height = int(binary.BigEndian.Uint32(data[4:8]))
	result.rgb = data[8:]
	if result.width < 1 || result.height < 1 || result.width > MaxSide || result.height > MaxSide {
		t.Fatalf("%s: invalid dimensions %dx%d", worker, result.width, result.height)
	}
	if len(result.rgb) != result.width*result.height*3 {
		t.Fatalf("%s: %d RGB bytes for %dx%d", worker, len(result.rgb), result.width, result.height)
	}
	return result, 0, stderr.String()
}

func requireProcessSuccess(t *testing.T, worker string, data []byte) processResult {
	t.Helper()
	result, code, stderr := runProcess(t, worker, data)
	if code != 0 {
		t.Fatalf("%s exited %d: %s", worker, code, stderr)
	}
	return result
}

func compareWorkers(t *testing.T, goWorker, zigWorker string, data []byte) (processResult, processResult) {
	t.Helper()
	goResult := requireProcessSuccess(t, goWorker, data)
	zigResult := requireProcessSuccess(t, zigWorker, data)
	if goResult.width != zigResult.width || goResult.height != zigResult.height {
		t.Fatalf("dimensions Go %dx%d, Zig %dx%d", goResult.width, goResult.height, zigResult.width, zigResult.height)
	}
	score := ssim(t, goResult, zigResult)
	if score < 0.98 {
		t.Fatalf("SSIM %.6f below 0.98", score)
	}
	return goResult, zigResult
}

func TestProcessIndependentPixels(t *testing.T) {
	goWorker, zigWorker := workers(t)
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	data := encodePNG(t, img)
	for _, worker := range []string{goWorker, zigWorker} {
		result := requireProcessSuccess(t, worker, data)
		want := []byte{255, 0, 0, 0, 255, 0}
		if result.width != 2 || result.height != 1 || !bytes.Equal(result.rgb, want) {
			t.Fatalf("%s: %dx%d %v, want 2x1 %v", worker, result.width, result.height, result.rgb, want)
		}
	}
}

func TestProcessCorpusAndEncodings(t *testing.T) {
	goWorker, zigWorker := workers(t)
	for _, kind := range kinds {
		for _, mp := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/%dMP", kind, mp), func(t *testing.T) {
				compareWorkers(t, goWorker, zigWorker, corpus(t, kind, mp))
			})
		}
	}
	for _, name := range []string{"progressive.jpg", "cmyk.jpg", "interlaced.png", "palette.png", "gray-trns.png"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			compareWorkers(t, goWorker, zigWorker, data)
		})
	}
	t.Run("16-bit PNG", func(t *testing.T) {
		img := image.NewNRGBA64(image.Rect(0, 0, 64, 48))
		for y := 0; y < 48; y++ {
			for x := 0; x < 64; x++ {
				img.SetNRGBA64(x, y, color.NRGBA64{R: uint16(x * 1023), G: uint16(y * 1301), B: 0x8080, A: uint16(0xffff - x*701)})
			}
		}
		compareWorkers(t, goWorker, zigWorker, encodePNG(t, img))
	})
}

func TestProcessAreaAverageIndependent(t *testing.T) {
	goWorker, zigWorker := workers(t)
	src := image.NewGray(image.Rect(0, 0, 777, 451))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 7919 >> 3)
	}
	data := encodePNG(t, src)
	overlap := func(a, n, b, m int) float64 {
		return float64(max(0, min((a+1)*m, (b+1)*n)-max(a*m, b*n)))
	}
	for _, worker := range []string{goWorker, zigWorker} {
		result := requireProcessSuccess(t, worker, data)
		if result.width != 320 || result.height != 186 {
			t.Fatalf("%s: size %dx%d, want 320x186", worker, result.width, result.height)
		}
		for dy := 0; dy < 186; dy++ {
			for dx := 0; dx < 320; dx++ {
				var sum, area float64
				for y := dy * 451 / 186; y <= min(450, (dy+1)*451/186); y++ {
					for x := dx * 777 / 320; x <= min(776, (dx+1)*777/320); x++ {
						weight := overlap(x, 777, dx, 320) * overlap(y, 451, dy, 186)
						sum += weight * float64(src.Pix[y*777+x])
						area += weight
					}
				}
				want := int(sum/area + 0.5)
				i := (dy*320 + dx) * 3
				for channel := 0; channel < 3; channel++ {
					if diff(result.rgb[i+channel], want) > 1 {
						t.Fatalf("%s: pixel (%d,%d) channel %d = %d, want %d", worker, dx, dy, channel, result.rgb[i+channel], want)
					}
				}
			}
		}
	}
}

func TestProcessOrientationsIndependent(t *testing.T) {
	goWorker, zigWorker := workers(t)
	src := solid(40, 20, color.NRGBA{B: 255, A: 255})
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	for orientation := uint16(1); orientation <= 8; orientation++ {
		t.Run(fmt.Sprint(orientation), func(t *testing.T) {
			data := withExif(encodeJPEG(t, src), orientation, binary.BigEndian)
			results := []processResult{requireProcessSuccess(t, goWorker, data), requireProcessSuccess(t, zigWorker, data)}
			for _, result := range results {
				w, h := 40, 20
				if orientation >= 5 {
					w, h = h, w
				}
				if result.width != w || result.height != h {
					t.Fatalf("orientation %d: size %dx%d, want %dx%d", orientation, result.width, result.height, w, h)
				}
				var total float64
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						sx, sy := independentOrientationSource(orientation, x, y, w, h)
						wantR, wantG, wantB, _ := src.At(sx, sy).RGBA()
						i := (y*w + x) * 3
						total += math.Abs(float64(result.rgb[i]) - float64(wantR>>8))
						total += math.Abs(float64(result.rgb[i+1]) - float64(wantG>>8))
						total += math.Abs(float64(result.rgb[i+2]) - float64(wantB>>8))
					}
				}
				if mean := total / float64(w*h*3); mean > 20 {
					t.Fatalf("orientation %d: mean independent pixel error %.2f", orientation, mean)
				}
			}
			compareWorkers(t, goWorker, zigWorker, data)
		})
	}
}

func independentOrientationSource(orientation uint16, x, y, w, h int) (int, int) {
	switch orientation {
	case 1:
		return x, y
	case 2:
		return w - 1 - x, y
	case 3:
		return w - 1 - x, h - 1 - y
	case 4:
		return x, h - 1 - y
	case 5:
		return y, x
	case 6:
		return y, w - 1 - x
	case 7:
		return h - 1 - y, w - 1 - x
	case 8:
		return h - 1 - y, x
	default:
		panic("orientation")
	}
}

func TestProcessNegativeInputs(t *testing.T) {
	goWorker, zigWorker := workers(t)
	valid := encodeJPEG(t, solid(300, 200, color.NRGBA{R: 1, A: 255}))
	cases := map[string]struct {
		data []byte
		code int
	}{
		"empty":       {nil, 2},
		"text":        {[]byte("not an image"), 2},
		"truncated":   {valid[:len(valid)/2], 2},
		"no pixels":   {pngHeader(64, 64, 8, 0), 2},
		"too large":   {pngHeader(6000, 5000, 8, 0), 3},
		"too heavy":   {pngHeader(6000, 4000, 16, 0), 3},
		"input + 1":   {make([]byte, (10<<20)+1), 3},
		"exact input": {make([]byte, 10<<20), 2},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			for _, worker := range []string{goWorker, zigWorker} {
				_, code, stderr := runProcess(t, worker, test.data)
				if code != test.code {
					t.Fatalf("%s exited %d, want %d: %s", worker, code, test.code, stderr)
				}
			}
		})
	}
}

func ssim(t *testing.T, first, second processResult) float64 {
	t.Helper()
	if first.width != second.width || first.height != second.height {
		t.Fatal("SSIM dimensions differ")
	}
	if bytes.Equal(first.rgb, second.rgb) {
		return 1
	}
	w, h := first.width, first.height
	if w < 11 || h < 11 {
		t.Fatal("SSIM requires at least 11x11 pixels for non-identical output")
	}
	kernel := [11]float64{}
	var sum float64
	for i := range kernel {
		x := float64(i - 5)
		kernel[i] = math.Exp(-x * x / (2 * 1.5 * 1.5))
		sum += kernel[i]
	}
	for i := range kernel {
		kernel[i] /= sum
	}
	const c1 = (0.01 * 255) * (0.01 * 255)
	const c2 = (0.03 * 255) * (0.03 * 255)
	var total float64
	for y := 5; y < h-5; y++ {
		for x := 5; x < w-5; x++ {
			var mu1, mu2, v1, v2, covariance float64
			for ky := -5; ky <= 5; ky++ {
				for kx := -5; kx <= 5; kx++ {
					weight := kernel[ky+5] * kernel[kx+5]
					i := ((y+ky)*w + x + kx) * 3
					y1 := 0.299*float64(first.rgb[i]) + 0.587*float64(first.rgb[i+1]) + 0.114*float64(first.rgb[i+2])
					y2 := 0.299*float64(second.rgb[i]) + 0.587*float64(second.rgb[i+1]) + 0.114*float64(second.rgb[i+2])
					mu1 += weight * y1
					mu2 += weight * y2
					v1 += weight * y1 * y1
					v2 += weight * y2 * y2
					covariance += weight * y1 * y2
				}
			}
			v1 -= mu1 * mu1
			v2 -= mu2 * mu2
			covariance -= mu1 * mu2
			total += ((2*mu1*mu2 + c1) * (2*covariance + c2)) / ((mu1*mu1 + mu2*mu2 + c1) * (v1 + v2 + c2))
		}
	}
	return total / float64((w-10)*(h-10))
}
