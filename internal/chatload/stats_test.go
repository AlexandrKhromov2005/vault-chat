package chatload

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSummaryUsesNearestRankPercentiles(t *testing.T) {
	var latencies Latencies
	for i := 100; i >= 1; i-- {
		latencies.Add(time.Duration(i) * time.Millisecond)
	}
	summary := latencies.Summary()
	require.Equal(t, 100, summary.Count)
	require.InDelta(t, 50, summary.P50, 1e-9)
	require.InDelta(t, 95, summary.P95, 1e-9)
	require.InDelta(t, 99, summary.P99, 1e-9)
	require.InDelta(t, 100, summary.Max, 1e-9)

	var single Latencies
	single.Add(1500 * time.Microsecond)
	require.Equal(t, Summary{Count: 1, P50: 1.5, P95: 1.5, P99: 1.5, Max: 1.5}, single.Summary())
	require.Equal(t, Summary{}, (&Latencies{}).Summary())
}

func TestCoefficientOfVariation(t *testing.T) {
	require.InDelta(t, 0, coefficientOfVariation([]float64{10, 10, 10}), 1e-9)
	require.InDelta(t, 0.1, coefficientOfVariation([]float64{9, 11}), 1e-9)
	require.InDelta(t, 0, coefficientOfVariation([]float64{7}), 1e-9)
	require.InDelta(t, 0, coefficientOfVariation(nil), 1e-9)
}

func TestSpreadIsTheRatioOfTheLargestToTheSmallest(t *testing.T) {
	require.InDelta(t, 1.5, spread([]float64{20, 30, 25}), 1e-9)
	require.InDelta(t, 1, spread([]float64{4}), 1e-9)
	require.InDelta(t, 1, spread(nil), 1e-9)
	require.InDelta(t, 1, spread([]float64{0, 0}), 1e-9)
}
