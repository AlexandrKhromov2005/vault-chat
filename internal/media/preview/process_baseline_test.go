//go:build linux

package preview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProcessBatch(t *testing.T) {
	path, worker := os.Getenv("PREVIEW_BATCH_FILE"), os.Getenv("PREVIEW_BATCH_WORKER")
	if path == "" || worker == "" {
		t.Skip("child of TestProcessBaselineReport")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := strconv.Atoi(os.Getenv("PREVIEW_BASELINE_RUNS"))
	for i := 0; i < 3+runs; i++ {
		result := requireProcessSuccess(t, worker, data)
		if i >= 3 {
			fmt.Println("NS", result.elapsed.Nanoseconds())
		}
	}
}

func processBatch(t *testing.T, path, worker string, runs int) []time.Duration {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessBatch$", "-test.v")
	cmd.Env = append(os.Environ(), "PREVIEW_BATCH_FILE="+path, "PREVIEW_BATCH_WORKER="+worker,
		"PREVIEW_BASELINE_RUNS="+strconv.Itoa(runs))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("batch %s: %v\n%s", worker, err, out)
	}
	var durations []time.Duration
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "NS "); ok {
			ns, _ := strconv.ParseInt(rest, 10, 64)
			durations = append(durations, time.Duration(ns))
		}
	}
	if len(durations) != runs {
		t.Fatalf("batch %s: %d durations, want %d\n%s", worker, len(durations), runs, out)
	}
	slices.Sort(durations)
	return durations
}

func processPeakMiB(t *testing.T, prefix []string, worker string, data []byte) float64 {
	t.Helper()
	args := append(prefix, "/usr/bin/time", "-f", "%M", worker)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", worker, err, stderr.String())
	}
	value := strings.TrimSpace(stderr.String())
	kib, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("%s: no peak RSS in %q", worker, value)
	}
	return kib / 1024
}

func TestProcessBaselineReport(t *testing.T) {
	if os.Getenv("PREVIEW_PROCESS_BASELINE") != "1" {
		t.Skip("set PREVIEW_PROCESS_BASELINE=1 to compare process workers")
	}
	goWorker, zigWorker := workers(t)
	runs, batches := 100, 5
	if value, err := strconv.Atoi(os.Getenv("PREVIEW_BASELINE_RUNS")); err == nil && value > 0 {
		runs = value
	}
	if value, err := strconv.Atoi(os.Getenv("PREVIEW_BASELINE_BATCHES")); err == nil && value > 0 {
		batches = value
	}
	dir := t.TempDir()
	t.Logf("%d batches × %d runs per worker; fresh process, stdin, decode, area-resize, RGB stdout", batches, runs)
	t.Logf("%-10s %4s %-22s %-22s %9s %9s %8s", "kind", "MP", "Go p50/p95/p99", "Zig p50/p95/p99", "p95 ratio", "RSS G/Z", "SSIM")
	for _, kind := range kinds {
		for _, mp := range megapixels {
			data := corpus(t, kind, mp)
			goResult, zigResult := compareWorkers(t, goWorker, zigWorker, data)
			score := ssim(t, goResult, zigResult)
			ext := map[bool]string{true: "jpg", false: "png"}[kind == "photo"]
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.%s", kind, mp, ext))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			durations := map[string][]time.Duration{"go": {}, "zig": {}}
			for batch := 0; batch < batches; batch++ {
				order := []string{"go", "zig"}
				if batch%2 == 1 {
					order[0], order[1] = order[1], order[0]
				}
				for _, name := range order {
					worker := map[string]string{"go": goWorker, "zig": zigWorker}[name]
					durations[name] = append(durations[name], processBatch(t, path, worker, runs)...)
				}
			}
			for _, values := range durations {
				slices.Sort(values)
			}
			rss := map[string]float64{}
			for _, name := range []string{"go", "zig"} {
				worker := map[string]string{"go": goWorker, "zig": zigWorker}[name]
				for range 3 {
					rss[name] = max(rss[name], processPeakMiB(t, nil, worker, data))
				}
			}
			ms := func(d time.Duration) string {
				return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 1, 64)
			}
			go95, zig95 := percentile(durations["go"], 0.95), percentile(durations["zig"], 0.95)
			t.Logf("%-10s %4d %-22s %-22s %9.2f %9s %8.4f", kind, mp,
				ms(percentile(durations["go"], 0.5))+"/"+ms(go95)+"/"+ms(percentile(durations["go"], 0.99)),
				ms(percentile(durations["zig"], 0.5))+"/"+ms(zig95)+"/"+ms(percentile(durations["zig"], 0.99)),
				float64(zig95)/float64(go95), fmt.Sprintf("%.0f/%.0f", rss["go"], rss["zig"]), score)
		}
	}
}

func TestProcessMemoryBudget(t *testing.T) {
	if os.Getenv("PREVIEW_PROCESS_MEMORY") != "1" {
		t.Skip("set PREVIEW_PROCESS_MEMORY=1 to measure the heaviest workers")
	}
	goWorker, zigWorker := workers(t)
	var prefix []string
	scope := []string{"systemd-run", "--user", "--scope", "--quiet", "-p", "MemorySwapMax=0",
		"-p", "MemoryMax=" + strconv.Itoa(memoryLimit)}
	if exec.Command(scope[0], append(scope[1:], "true")...).Run() == nil {
		prefix = scope
	} else {
		t.Log("no delegated cgroup: checking the measured peak only")
	}
	for name, input := range memoryInputs(t) {
		data := input()
		for _, item := range []struct {
			name, path string
		}{{"Go", goWorker}, {"Zig", zigWorker}} {
			t.Run(name+"/"+item.name, func(t *testing.T) {
				peak := processPeakMiB(t, prefix, item.path, data)
				if peak > float64(memoryLimit>>20) {
					t.Errorf("peak %.0f MiB exceeds %d MiB", peak, memoryLimit>>20)
				}
				t.Logf("peak %.0f MiB", peak)
			})
		}
	}
}
