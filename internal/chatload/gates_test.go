package chatload

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func gates() Gates {
	return Gates{
		SendP95Ms: 100, SendP99Ms: 200, ReadP95Ms: 100, ReadP99Ms: 200, DeliveryP95Ms: 150, DeliveryP99Ms: 300,
		MaxErrorRate: 0.001, MinThroughputRatio: 0.98, MinDeliveryRatio: 0.999, MaxThroughputCV: 0.05,
		MaxP95Spread: 1.5, MaxMemoryRatio: 0.9, MaxReconnectMs: 2000,
	}
}

func passingPhase(name string) PhaseReport {
	return PhaseReport{
		Name: name, ThroughputRatio: 1, Throughput: 40, DeliveryRatio: 1, Requests: 1000, Server: Server{Scrapes: 60},
		Send:     Summary{Count: 10, P50: 10, P95: 90, P99: 150, Max: 160},
		Read:     Summary{Count: 10, P50: 10, P95: 90, P99: 150, Max: 160},
		Delivery: Summary{Count: 10, P50: 10, P95: 140, P99: 250, Max: 260},
		Resources: &Resources{Containers: map[string]ContainerUsage{
			"chat": {CPUCores: 0.3, MemoryPeakBytes: 100, MemoryLimitBytes: 1000, MemoryRatio: 0.1},
		}},
	}
}

func passingReport() Report {
	reconnect := passingPhase("reconnect")
	reconnect.Reconnect = &ReconnectReport{Users: 2, Reconnected: 2, Reconnect: Summary{Count: 2, P50: 100, P95: 300, P99: 300, Max: 300}}
	return Report{
		Profile:   Profile{Gates: gates()},
		Series:    []PhaseReport{passingPhase("series-1"), passingPhase("series-2")},
		Reconnect: reconnect,
	}
}

func gateNames(violations []Violation) []string {
	names := make([]string, 0, len(violations))
	for _, v := range violations {
		names = append(names, v.Phase+"/"+v.Gate)
	}
	return names
}

func TestEvaluatePassesAReportWithinTheGates(t *testing.T) {
	report := passingReport()
	report.Evaluate()
	require.Empty(t, report.Violations)
	require.True(t, report.Passed)
}

func TestEvaluateReportsEachViolatedGate(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*Report)
		want   []string
	}{
		"send p95": {func(r *Report) { r.Series[0].Send.P95 = 101 }, []string{"series-1/send_p95_ms"}},
		"send p99": {func(r *Report) { r.Series[1].Send.P99 = 201 }, []string{"series-2/send_p99_ms"}},
		"read p95": {func(r *Report) { r.Series[0].Read.P95 = 101 }, []string{"series-1/read_p95_ms"}},
		"delivery": {func(r *Report) { r.Series[0].Delivery.P99 = 301 }, []string{"series-1/delivery_p99_ms"}},
		"errors": {func(r *Report) {
			r.Series[0].Errors = map[string]int64{"http_503": 2}
			r.Series[0].ErrorRate = 0.002
		}, []string{"series-1/max_error_rate"}},
		"throughput":       {func(r *Report) { r.Series[0].ThroughputRatio = 0.9 }, []string{"series-1/min_throughput_ratio"}},
		"lost events":      {func(r *Report) { r.Series[0].DeliveryRatio = 0.99 }, []string{"series-1/min_delivery_ratio"}},
		"live duplicate":   {func(r *Report) { r.Series[0].LiveDuplicates = 1 }, []string{"series-1/live_duplicates"}},
		"integrity":        {func(r *Report) { r.Reconnect.Integrity.ViewMissing = 3 }, []string{"reconnect/integrity"}},
		"dropped events":   {func(r *Report) { r.Series[1].Server.EventsDropped = 1 }, []string{"series-2/events_dropped"}},
		"publish failures": {func(r *Report) { r.Series[1].Server.PublishFailures = 1 }, []string{"series-2/events_publish_failures"}},
		"unexpected close": {func(r *Report) { r.Series[1].UnexpectedCloses = map[string]int64{"4002": 1} }, []string{"series-2/unexpected_closes"}},
		"memory": {func(r *Report) {
			r.Series[0].Resources.Containers["chat"] = ContainerUsage{MemoryRatio: 0.95}
		}, []string{"series-1/max_memory_ratio:chat"}},
		"restart": {func(r *Report) {
			r.Series[0].Resources.Containers["chat"] = ContainerUsage{Restarts: 1}
		}, []string{"series-1/restarts:chat"}},
		"oom": {func(r *Report) {
			r.Series[0].Resources.Containers["chat"] = ContainerUsage{OOMKilled: true}
		}, []string{"series-1/oom_killed:chat"}},
		"metrics unreadable": {func(r *Report) { r.Series[0].Server.ScrapeErrors = 1 }, []string{"series-1/metrics"}},
		"missing resources":  {func(r *Report) { r.Series[0].Resources = nil }, []string{"series-1/resources"}},
		"reconnect slow":     {func(r *Report) { r.Reconnect.Reconnect.Reconnect.Max = 2001 }, []string{"reconnect/max_reconnect_ms"}},
		"reconnect failed":   {func(r *Report) { r.Reconnect.Reconnect.Reconnected = 1 }, []string{"reconnect/reconnected"}},
		"throughput cv": {func(r *Report) {
			r.Series[0].Throughput, r.Series[1].Throughput = 30, 50
		}, []string{"series/max_throughput_cv"}},
		"p95 spread": {func(r *Report) {
			r.Series[0].Delivery.P95, r.Series[1].Delivery.P95 = 40, 100
		}, []string{"series/max_p95_spread:delivery"}},
		"no measurement": {func(r *Report) { r.Series[0].Send.Count = 0 }, []string{"series-1/send_count"}},
	} {
		t.Run(name, func(t *testing.T) {
			report := passingReport()
			tc.change(&report)
			report.Evaluate()
			require.Equal(t, tc.want, gateNames(report.Violations))
			require.False(t, report.Passed)
		})
	}
}

func TestEvaluateLetsClientsMissLiveEventsOnlyWhileReconnecting(t *testing.T) {
	report := passingReport()
	report.Reconnect.DeliveryRatio = 0.5
	report.Evaluate()
	require.Empty(t, report.Violations, "catch-up recovers them; integrity is checked instead")

	report = passingReport()
	report.Reconnect.Send.P99 = 201
	report.Evaluate()
	require.Equal(t, []string{"reconnect/send_p99_ms"}, gateNames(report.Violations))
}
