package chatload

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Metrics holds the samples of one Prometheus text exposition by metric name.
type Metrics map[string][]sample

type sample struct {
	labels map[string]string
	value  float64
}

// ParseMetrics reads the Prometheus text format. Comments are skipped; any
// other malformed line is an error, so that a renamed or broken exporter
// does not silently read as zero.
func ParseMetrics(r io.Reader) (Metrics, error) {
	metrics := Metrics{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, s, err := parseSample(text)
		if err != nil {
			return nil, fmt.Errorf("metrics line %d: %w", line, err)
		}
		metrics[name] = append(metrics[name], s)
	}
	return metrics, scanner.Err()
}

func parseSample(text string) (string, sample, error) {
	s := sample{labels: map[string]string{}}
	end := strings.IndexAny(text, "{ ")
	if end <= 0 {
		return "", s, fmt.Errorf("no value")
	}
	name, rest := text[:end], text[end:]
	if strings.HasPrefix(rest, "{") {
		var err error
		if rest, err = parseLabels(rest[1:], s.labels); err != nil {
			return "", s, err
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || len(fields) > 2 {
		return "", s, fmt.Errorf("no value")
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", s, fmt.Errorf("value: %w", err)
	}
	s.value = value
	return name, s, nil
}

// parseLabels reads `name="value",...}` and returns the text after `}`.
func parseLabels(text string, labels map[string]string) (string, error) {
	for {
		text = strings.TrimLeft(text, " ,")
		if strings.HasPrefix(text, "}") {
			return text[1:], nil
		}
		eq := strings.Index(text, "=")
		if eq <= 0 || len(text) < eq+2 || text[eq+1] != '"' {
			return "", fmt.Errorf("malformed label")
		}
		name := strings.TrimSpace(text[:eq])
		var value strings.Builder
		i := eq + 2
		for ; i < len(text) && text[i] != '"'; i++ {
			if text[i] == '\\' && i+1 < len(text) {
				i++
				switch text[i] {
				case 'n':
					value.WriteByte('\n')
				default:
					value.WriteByte(text[i])
				}
				continue
			}
			value.WriteByte(text[i])
		}
		if i == len(text) {
			return "", fmt.Errorf("unterminated label value")
		}
		labels[name] = value.String()
		text = text[i+1:]
		if !strings.HasPrefix(text, ",") && !strings.HasPrefix(text, "}") {
			return "", fmt.Errorf("malformed label list")
		}
	}
}

// Sum adds the samples of name whose labels include every pair of match.
func (m Metrics) Sum(name string, match map[string]string) float64 {
	return m.SumWhere(name, func(labels map[string]string) bool {
		for key, want := range match {
			if labels[key] != want {
				return false
			}
		}
		return true
	})
}

// SumWhere adds the samples of name accepted by keep; NaN samples are skipped.
func (m Metrics) SumWhere(name string, keep func(map[string]string) bool) float64 {
	var total float64
	for _, s := range m[name] {
		if !math.IsNaN(s.value) && keep(s.labels) {
			total += s.value
		}
	}
	return total
}
