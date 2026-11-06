package preview

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// memoryLimit bounds the resident memory of a process that makes one
// preview: MaxDecoded, an input of up to 10 MiB and the Go runtime.
const memoryLimit = MaxDecoded + 40<<20

// memoryInputs are the heaviest inputs within the budget of each kind.
func memoryInputs(t testing.TB) map[string]func() []byte {
	return map[string]func() []byte{
		"PNG 8-bit RGBA 24 MP":  func() []byte { return encodePNG(t, gradient(6000, 4000, 8, true)) },
		"PNG 16-bit RGBA 12 MP": func() []byte { return encodePNG(t, gradient(4000, 3000, 16, true)) },
		"PNG 16-bit RGB 12 MP":  func() []byte { return encodePNG(t, gradient(4000, 3000, 16, false)) },
		"PNG interlaced 12 MP":  func() []byte { return interlacedPNG(t, 4000, 3000) },
		"PNG 6 900 000×1":       func() []byte { return encodePNG(t, gradient(6_900_000, 1, 8, true)) },
		"PNG 1×24 000 000":      func() []byte { return encodePNG(t, gradient(1, 24_000_000, 8, true)) },
		"JPEG 4:2:0 24 MP":      func() []byte { return encodeJPEG(t, gradient(6000, 4000, 8, false)) },
	}
}

// TestMemoryChild is the child of TestMakeStaysWithinTheMemoryLimit and
// does nothing on its own. With PREVIEW_MEMORY_CASE it writes that input to
// PREVIEW_MEMORY_FILE; without, it makes the preview of the file and prints
// its peak resident memory.
func TestMemoryChild(t *testing.T) {
	path := os.Getenv("PREVIEW_MEMORY_FILE")
	if path == "" {
		t.Skip("child process of TestMakeStaysWithinTheMemoryLimit")
	}
	if name := os.Getenv("PREVIEW_MEMORY_CASE"); name != "" {
		if err := os.WriteFile(path, memoryInputs(t)[name](), 0o600); err != nil {
			t.Fatal(err)
		}
		return
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
		if strings.HasPrefix(line, "VmHWM:") {
			fmt.Println("peak", strings.Fields(line)[1])
		}
	}
}

// The heaviest inputs within the budget of each kind get a preview in a
// separate process, and its peak resident memory stays within memoryLimit.
// Where systemd can delegate a cgroup, the child also runs under that limit
// with swap off, as a container would; RLIMIT_DATA does not work for this,
// because it counts address space the Go runtime maps but never touches.
func TestMakeStaysWithinTheMemoryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes several 24-megapixel images")
	}
	var prefix []string
	scope := []string{"systemd-run", "--user", "--scope", "--quiet", "-p", "MemorySwapMax=0",
		"-p", "MemoryMax=" + strconv.Itoa(memoryLimit)}
	if exec.Command(scope[0], append(scope[1:], "true")...).Run() == nil {
		prefix = scope
	} else {
		t.Log("no delegated cgroup: checking the measured peak only")
	}
	// The race detector multiplies memory and time, so the child that
	// generates the inputs and makes the previews is built without it.
	dir := t.TempDir()
	child := filepath.Join(dir, "child.test")
	build := exec.Command("go", "test", "-c", "-o", child, ".")
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the child: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "input")
	run := func(prefix []string, env ...string) ([]byte, error) {
		args := append(prefix, child, "-test.run=^TestMemoryChild$", "-test.v")
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(os.Environ(), append(env, "PREVIEW_MEMORY_FILE="+path)...)
		return cmd.CombinedOutput()
	}
	for name := range memoryInputs(t) {
		if out, err := run(nil, "PREVIEW_MEMORY_CASE="+name); err != nil {
			t.Fatalf("%s: generate: %v\n%s", name, err, out)
		}
		out, err := run(prefix)
		_, peak, _ := strings.Cut(string(out), "peak ")
		kib, _ := strconv.Atoi(strings.Fields(peak + " 0")[0])
		if err != nil || kib == 0 || kib<<10 > memoryLimit {
			t.Errorf("%s: peak %d KiB, limit %d KiB, %v\n%s", name, kib, memoryLimit>>10, err, out)
			continue
		}
		t.Logf("%s: peak %d KiB of %d KiB", name, kib, memoryLimit>>10)
	}
}

// gradient is a w×h image of the given bit depth, translucent if alpha.
func gradient(w, h, depth int, alpha bool) image.Image {
	a := func(x int) uint8 {
		if alpha {
			return 255 - uint8(x%128)
		}
		return 255
	}
	if depth == 16 {
		img := image.NewNRGBA64(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.SetNRGBA64(x, y, color.NRGBA64{uint16(x * 9), uint16(y * 11), uint16(x ^ y), uint16(a(x)) * 257})
			}
		}
		return img
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), a(x)})
		}
	}
	return img
}

// interlacedPNG encodes an 8-bit RGB gradient with Adam7 interlacing, which
// image/png decodes but does not write.
func interlacedPNG(t testing.TB, w, h int) []byte {
	var raw bytes.Buffer
	z := zlib.NewWriter(&raw)
	for _, p := range [7][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}} {
		for y := p[1]; y < h; y += p[3] {
			if p[0] >= w {
				break
			}
			row := []byte{0} // filter None
			for x := p[0]; x < w; x += p[2] {
				row = append(row, uint8(x), uint8(y), uint8(x+y))
			}
			if _, err := z.Write(row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	ihdr := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(w)), uint32(h))
	out := []byte("\x89PNG\r\n\x1a\n")
	for _, chunk := range []struct {
		kind string
		data []byte
	}{{"IHDR", append(ihdr, 8, 2, 0, 0, 1)}, {"IDAT", raw.Bytes()}, {"IEND", nil}} {
		body := append([]byte(chunk.kind), chunk.data...)
		out = binary.BigEndian.AppendUint32(out, uint32(len(chunk.data)))
		out = binary.BigEndian.AppendUint32(append(out, body...), crc32.ChecksumIEEE(body))
	}
	return out
}
