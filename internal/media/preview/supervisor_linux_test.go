//go:build linux

package preview

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

func supervisorTestWorker(mode string, args ...string) Worker {
	return Worker{
		Path:    os.Args[0],
		Args:    append([]string{"-test.run=^TestSupervisorHelper$", "--", mode}, args...),
		Timeout: 2 * time.Second,
	}
}

func TestWorkerDecodesWithExpectedDimensions(t *testing.T) {
	worker := supervisorTestWorker("success")
	png := encodePNG(t, solid(40, 20, color.NRGBA{A: 255}))
	for name, data := range map[string][]byte{
		"png":              png,
		"EXIF orientation": withExif(encodeJPEG(t, solid(40, 20, color.NRGBA{A: 255})), 6, binary.BigEndian),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := worker.Decode(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			wantWidth, wantHeight := 40, 20
			if name == "EXIF orientation" {
				wantWidth, wantHeight = wantHeight, wantWidth
			}
			if got.Width != wantWidth || got.Height != wantHeight || len(got.RGB) != wantWidth*wantHeight*3 {
				t.Fatalf("response %dx%d, %d RGB bytes", got.Width, got.Height, len(got.RGB))
			}
		})
	}
}

func TestWorkerRunsRealDecoders(t *testing.T) {
	goWorker, zigWorker := workers(t)
	for _, kind := range kinds {
		data := corpus(t, kind, 1)
		expectedWidth, expectedHeight, expectedRGB, err := DecodeRGB(data)
		if err != nil {
			t.Fatal(err)
		}
		expected := processResult{width: expectedWidth, height: expectedHeight, rgb: expectedRGB}
		for name, path := range map[string]string{"Go": goWorker, "Zig": zigWorker} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				worker := Worker{Path: path, Timeout: 5 * time.Second}
				got, err := worker.Decode(context.Background(), data)
				if err != nil {
					t.Fatal(err)
				}
				actual := processResult{width: got.Width, height: got.Height, rgb: got.RGB}
				if got.Width != expectedWidth || got.Height != expectedHeight || ssim(t, expected, actual) < 0.98 {
					t.Fatalf("response %dx%d, want %dx%d with SSIM >= 0.98", got.Width, got.Height, expectedWidth, expectedHeight)
				}
			})
		}
	}
}

func TestWorkerMapsExitCodesAndPreflightFailures(t *testing.T) {
	data := encodePNG(t, solid(2, 1, color.NRGBA{A: 255}))
	for mode, want := range map[string]error{"exit2": ErrInvalid, "exit3": ErrSkipped, "exit4": ErrWorker} {
		if _, err := supervisorTestWorker(mode).Decode(context.Background(), data); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", mode, err, want)
		}
	}
	missing := Worker{Path: filepath.Join(t.TempDir(), "missing"), Timeout: time.Second}
	if _, err := missing.Decode(context.Background(), []byte("not an image")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid input reached the worker: %v", err)
	}
	if _, err := missing.Decode(context.Background(), make([]byte, MaxInput+1)); !errors.Is(err, ErrSkipped) {
		t.Fatalf("oversized input: %v, want ErrSkipped", err)
	}
	if _, err := missing.Decode(context.Background(), data); !errors.Is(err, ErrWorker) {
		t.Fatalf("missing worker: %v, want ErrWorker", err)
	}
}

func TestWorkerRejectsMalformedResponses(t *testing.T) {
	data := encodePNG(t, solid(2, 1, color.NRGBA{A: 255}))
	for _, mode := range []string{"short", "extra", "wrong-size", "zero", "oversize", "huge", "noisy-stderr"} {
		t.Run(mode, func(t *testing.T) {
			if _, err := supervisorTestWorker(mode).Decode(context.Background(), data); !errors.Is(err, ErrWorker) {
				t.Fatalf("%v, want ErrWorker", err)
			}
		})
	}
}

func TestWorkerUsesEmptyEnvironment(t *testing.T) {
	data := encodePNG(t, solid(2, 1, color.NRGBA{A: 255}))
	if _, err := supervisorTestWorker("empty-env").Decode(context.Background(), data); err != nil {
		t.Fatalf("worker inherited environment: %v", err)
	}
}

func TestWorkerRunsIndependentProcessesConcurrently(t *testing.T) {
	data := encodePNG(t, solid(20, 10, color.NRGBA{A: 255}))
	worker := supervisorTestWorker("success")
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := worker.Decode(context.Background(), data)
			if err == nil && (got.Width != 20 || got.Height != 10 || len(got.RGB) != 600) {
				err = fmt.Errorf("invalid response %dx%d, %d bytes", got.Width, got.Height, len(got.RGB))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestWorkerDeadlineKillsProcessGroup(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	worker := supervisorTestWorker("spawn", pidPath)
	worker.Timeout = time.Second
	started := time.Now()
	_, err := worker.Decode(context.Background(), encodePNG(t, solid(2, 1, color.NRGBA{A: 255})))
	if !errors.Is(err, ErrWorker) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v, want ErrWorker and context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("deadline took %v", elapsed)
	}
	raw, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(string(raw))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("child process %d survived the worker deadline", pid)
	} else if !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
}

func TestWorkerCPULimit(t *testing.T) {
	worker := supervisorTestWorker("busy")
	worker.Timeout = 10 * time.Second
	started := time.Now()
	_, err := worker.Decode(context.Background(), encodePNG(t, solid(2, 1, color.NRGBA{A: 255})))
	if !errors.Is(err, ErrWorker) {
		t.Fatalf("busy worker: %v, want ErrWorker", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CPU limit did not stop the worker before its wall-clock deadline: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("CPU-limited worker took %v", elapsed)
	}
}

func TestSupervisorHelper(t *testing.T) {
	mode, ok := supervisorHelperMode()
	if !ok {
		return
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, MaxInput+1))
	if err != nil || len(data) > MaxInput {
		os.Exit(3)
	}
	switch mode {
	case "exit2":
		os.Exit(2)
	case "exit3":
		os.Exit(3)
	case "exit4":
		os.Exit(4)
	case "short":
		_, _ = os.Stdout.Write([]byte{0, 0})
		os.Exit(0)
	case "extra":
		writeSupervisorHelperResponse(t, 2, 1, make([]byte, 7))
		os.Exit(0)
	case "wrong-size":
		writeSupervisorHelperResponse(t, 1, 2, make([]byte, 6))
		os.Exit(0)
	case "zero":
		writeSupervisorHelperResponse(t, 0, 0, nil)
		os.Exit(0)
	case "oversize":
		writeSupervisorHelperResponse(t, MaxSide+1, 1, make([]byte, (MaxSide+1)*3))
		os.Exit(0)
	case "huge":
		_, _ = os.Stdout.Write(make([]byte, maxWorkerResponse+1))
		os.Exit(0)
	case "noisy-stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'x'}, 1<<20))
		os.Exit(4)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "spawn":
		child := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelper$", "--", "sleep")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		if err := os.WriteFile(supervisorHelperArgument(t, 2), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "busy":
		for {
			runtime.Gosched()
		}
	}
	if mode == "empty-env" && len(os.Environ()) != 0 {
		os.Exit(4)
	}
	if mode != "success" && mode != "empty-env" {
		os.Exit(4)
	}
	w, h, rgb, err := DecodeRGB(data)
	if errors.Is(err, ErrSkipped) {
		os.Exit(3)
	}
	if err != nil {
		os.Exit(2)
	}
	writeSupervisorHelperResponse(t, w, h, rgb)
	os.Exit(0)
}

func supervisorHelperMode() (string, bool) {
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			return os.Args[i+1], true
		}
	}
	return "", false
}

func supervisorHelperArgument(t *testing.T, offset int) string {
	t.Helper()
	for i, arg := range os.Args {
		if arg == "--" && i+offset < len(os.Args) {
			return os.Args[i+offset]
		}
	}
	t.Fatal("missing helper argument")
	return ""
}

func writeSupervisorHelperResponse(t *testing.T, w, h int, rgb []byte) {
	t.Helper()
	response := make([]byte, 8, 8+len(rgb))
	binary.BigEndian.PutUint32(response, uint32(w))
	binary.BigEndian.PutUint32(response[4:], uint32(h))
	response = append(response, rgb...)
	for len(response) > 0 {
		n, err := os.Stdout.Write(response)
		if err != nil {
			os.Exit(4)
		}
		response = response[n:]
	}
}
