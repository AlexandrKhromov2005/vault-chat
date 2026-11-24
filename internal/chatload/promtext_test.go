package chatload

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const exposition = `# HELP vault_http_requests_total HTTP requests.
# TYPE vault_http_requests_total counter
vault_http_requests_total{kind="api",method="POST",status="200"} 120
vault_http_requests_total{kind="api",method="GET",status="200"} 30
vault_http_requests_total{kind="api",method="POST",status="503"} 2
vault_http_requests_total{kind="websocket",method="GET",status="101"} 7
vault_event_queue_pending 3
vault_odd{path="a \"quoted\" \\ value",x="y"} 1.5e+02
vault_histogram_bucket{le="+Inf"} 4
vault_nan NaN
`

func TestParseMetricsSumsMatchingSeries(t *testing.T) {
	metrics, err := ParseMetrics(strings.NewReader(exposition))
	require.NoError(t, err)
	require.InDelta(t, 150, metrics.Sum("vault_http_requests_total", map[string]string{"kind": "api", "status": "200"}), 1e-9)
	require.InDelta(t, 159, metrics.Sum("vault_http_requests_total", nil), 1e-9)
	require.InDelta(t, 2, metrics.SumWhere("vault_http_requests_total", func(labels map[string]string) bool {
		return labels["kind"] == "api" && strings.HasPrefix(labels["status"], "5")
	}), 1e-9)
	require.InDelta(t, 3, metrics.Sum("vault_event_queue_pending", nil), 1e-9)
	require.InDelta(t, 150, metrics.Sum("vault_odd", map[string]string{"path": `a "quoted" \ value`}), 1e-9)
	require.InDelta(t, 4, metrics.Sum("vault_histogram_bucket", map[string]string{"le": "+Inf"}), 1e-9)
	require.InDelta(t, 0, metrics.Sum("vault_nan", nil), 1e-9, "NaN samples are skipped")
	require.InDelta(t, 0, metrics.Sum("vault_missing", nil), 1e-9)
}

func TestParseMetricsRejectsMalformedLines(t *testing.T) {
	for _, text := range []string{
		"vault_x{kind=\"api\" 1\n",
		"vault_x{kind=api} 1\n",
		"vault_x\n",
		"vault_x one\n",
		"vault_x{kind=\"unterminated} 1\n",
	} {
		_, err := ParseMetrics(strings.NewReader(text))
		require.Error(t, err, text)
	}
}
