//go:build linux

package preview

// The Go baseline of ADR 0006 on a deterministic synthetic corpus. Benchmarks
// run with `go test -bench`; the report with percentiles, peak RSS and an
// optional libvips reference runs only when PREVIEW_BASELINE=1:
//
//	PREVIEW_BASELINE=1 go test -run TestBaselineReport -v -timeout 60m ./internal/media/preview/
//
// Peak RSS comes from /proc and GNU time (`/usr/bin/time`): the rusage of a
// child started by Go reports the parent's peak, because the child shares the
// parent's memory until exec.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	kinds       = []string{"photo", "screenshot", "alpha"}
	megapixels  = []int{1, 4, 12, 24}
	corpusCache sync.Map
)

// corpus returns a deterministic 3:2 image of the given kind: a JPEG photo
// with gradients and noise, an opaque PNG with flat areas and sharp edges, or
// a PNG with a transparency gradient.
func corpus(tb testing.TB, kind string, mp int) []byte {
	key := fmt.Sprint(kind, mp)
	if data, ok := corpusCache.Load(key); ok {
		return data.([]byte)
	}
	w := int(math.Sqrt(float64(mp) * 1e6 * 1.5))
	h := w * 2 / 3
	rng := rand.New(rand.NewPCG(uint64(mp), 7))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.NRGBA
			switch kind {
			case "photo":
				n := rng.IntN(24)
				c = color.NRGBA{uint8(min(255, x*255/w+n)), uint8(min(255, y*255/h+n)), uint8((x+y)*127/(w+h) + n), 255}
			case "screenshot":
				c = color.NRGBA{uint8(x / 97 * 40), uint8(y / 61 * 50), 220, 255}
				if x%160 == 0 || y%48 == 0 {
					c = color.NRGBA{20, 20, 20, 255}
				}
			default:
				c = color.NRGBA{uint8(x * 255 / w), 90, 200, uint8(y * 255 / h)}
			}
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
	var buf bytes.Buffer
	var err error
	if kind == "photo" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		tb.Fatal(err)
	}
	corpusCache.Store(key, buf.Bytes())
	return buf.Bytes()
}

func BenchmarkMake(b *testing.B) {
	for _, kind := range kinds {
		for _, mp := range megapixels {
			data := corpus(b, kind, mp)
			b.Run(fmt.Sprintf("%s/%dMP", kind, mp), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					if _, err := Make(data); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// TestBaselineItem is the child process of the report: one Make of one file,
// then the process's own peak RSS from /proc.
func TestBaselineItem(t *testing.T) {
	path := os.Getenv("PREVIEW_ITEM")
	if path == "" {
		t.Skip("child of TestBaselineReport")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Make(data); err != nil {
		t.Fatal(err)
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "VmHWM:" {
			fmt.Println("PEAK_KIB", fields[1])
		}
	}
}

// peakMiB runs a command and returns the peak RSS it reports, in MiB: a line
// "PEAK_KIB <n>" from TestBaselineItem, or else the last line of GNU time -f %M.
func peakMiB(t *testing.T, cmd *exec.Cmd) float64 {
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", cmd.Args, err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	value := lines[len(lines)-1]
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "PEAK_KIB "); ok {
			value = rest
		}
	}
	kib, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		t.Fatalf("%v: no peak RSS in output\n%s", cmd.Args, out)
	}
	return kib / 1024
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	return sorted[min(len(sorted)-1, int(math.Ceil(p*float64(len(sorted))))-1)]
}

func TestBaselineReport(t *testing.T) {
	if os.Getenv("PREVIEW_BASELINE") != "1" {
		t.Skip("set PREVIEW_BASELINE=1 to measure the Go baseline")
	}
	runs := 100 // enough runs for p99 to differ from the maximum
	if v, err := strconv.Atoi(os.Getenv("PREVIEW_BASELINE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	vips, _ := exec.LookPath("vipsthumbnail")
	dir := t.TempDir()
	idle := peakMiB(t, exec.Command("/usr/bin/time", "-f", "%M", os.Args[0], "-test.run=^TestBaselineItem$"))
	t.Logf("runs=%d, idle child RSS %.0f MiB, vipsthumbnail=%q", runs, idle, vips)
	t.Logf("%-10s %4s %8s %8s %8s %8s %8s %7s %9s %9s", "kind", "MP", "input", "p50", "p95", "p99", "RSS MiB", "output", "vips p50", "vips MiB")
	for _, kind := range kinds {
		for _, mp := range megapixels {
			data := corpus(t, kind, mp)
			var durations []time.Duration
			var out []byte
			for range runs {
				start := time.Now()
				var err error
				if out, err = Make(data); err != nil {
					t.Fatal(err)
				}
				durations = append(durations, time.Since(start))
			}
			slices.Sort(durations)
			ext := map[bool]string{true: "jpg", false: "png"}[kind == "photo"]
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.%s", kind, mp, ext))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestBaselineItem$")
			child.Env = append(os.Environ(), "PREVIEW_ITEM="+path)
			rss := peakMiB(t, child)
			vipsTime, vipsRSS := "-", "-"
			if vips != "" {
				var best time.Duration
				var peak float64
				for i := range 5 {
					start := time.Now()
					cmd := exec.Command("/usr/bin/time", "-f", "%M", vips, path, "--size", "320", "-o", filepath.Join(dir, "v.jpg[Q=80,strip]"))
					peak = max(peak, peakMiB(t, cmd))
					if d := time.Since(start); i == 0 || d < best {
						best = d
					}
				}
				vipsTime, vipsRSS = best.Round(time.Millisecond).String(), fmt.Sprintf("%.0f", peak)
			}
			t.Logf("%-10s %4d %7dK %8v %8v %8v %8.0f %6dK %9s %9s", kind, mp, len(data)>>10,
				percentile(durations, 0.5).Round(time.Millisecond), percentile(durations, 0.95).Round(time.Millisecond),
				percentile(durations, 0.99).Round(time.Millisecond), rss, len(out)>>10, vipsTime, vipsRSS)
		}
	}
	// Throughput of two parallel jobs on the 12-megapixel photo.
	data := corpus(t, "photo", 12)
	start := time.Now()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range runs {
				if _, err := Make(data); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	t.Logf("2 parallel jobs, 12 MP photo: %.2f previews/s", float64(2*runs)/time.Since(start).Seconds())
}
