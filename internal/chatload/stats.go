// Package chatload drives the reproducible Chat/Web load acceptance
// (docs/load/chat.md): synthetic users talk to the Gateway over TLS like the
// browser client, and the package measures latency, throughput, errors and the
// integrity of what every client ends up with.
package chatload

import (
	"math"
	"slices"
	"sync"
	"time"
)

// Latencies collects the durations of one kind of operation. It is safe for
// concurrent use.
type Latencies struct {
	mu     sync.Mutex
	values []time.Duration
}

// Add records one duration.
func (l *Latencies) Add(d time.Duration) {
	l.mu.Lock()
	l.values = append(l.values, d)
	l.mu.Unlock()
}

// Summary is a latency distribution in milliseconds.
type Summary struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

// Summary returns nearest-rank percentiles of the recorded durations.
func (l *Latencies) Summary() Summary {
	l.mu.Lock()
	sorted := slices.Clone(l.values)
	l.mu.Unlock()
	if len(sorted) == 0 {
		return Summary{}
	}
	slices.Sort(sorted)
	return Summary{
		Count: len(sorted),
		P50:   milliseconds(percentile(sorted, 50)),
		P95:   milliseconds(percentile(sorted, 95)),
		P99:   milliseconds(percentile(sorted, 99)),
		Max:   milliseconds(sorted[len(sorted)-1]),
	}
}

// percentile returns the nearest-rank percentile of a sorted, non-empty slice.
func percentile(sorted []time.Duration, percent int) time.Duration {
	index := (len(sorted)*percent+99)/100 - 1
	return sorted[max(0, index)]
}

func milliseconds(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// coefficientOfVariation is the population standard deviation divided by
// the mean; it is zero for fewer than two values or a zero mean.
func coefficientOfVariation(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	if mean == 0 {
		return 0
	}
	var squares float64
	for _, v := range values {
		squares += (v - mean) * (v - mean)
	}
	return math.Sqrt(squares/float64(len(values))) / mean
}

// spread is the largest value divided by the smallest; it is one when there
// is nothing to compare.
func spread(values []float64) float64 {
	if len(values) < 2 {
		return 1
	}
	low, high := slices.Min(values), slices.Max(values)
	if low <= 0 {
		return 1
	}
	return high / low
}
