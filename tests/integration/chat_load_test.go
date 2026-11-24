package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chatload"
)

// snapshots stands in for the orchestrator: it answers every mark with a
// snapshot, so that the gates on resources can be evaluated.
type snapshots struct{ marks []string }

func (s *snapshots) Mark(phase, edge string) (*chatload.Snapshot, error) {
	s.marks = append(s.marks, phase+"/"+edge)
	return &chatload.Snapshot{
		Time:       float64(time.Now().UnixNano()) / 1e9,
		Containers: map[string]chatload.ContainerSnapshot{"chat": {Memory: 1, MemoryLimit: 100}},
	}, nil
}

// TestChatLoadGeneratorRunsAProfileEndToEnd runs the load generator of
// tests/load with a small profile against the in-process stack: real Auth,
// Chat and Gateway, PostgreSQL and Redis. It checks that every message the
// generator sent reached every member, live or through catch-up after the
// reconnect, and that retries with the same client message id stored nothing
// twice.
func TestChatLoadGeneratorRunsAProfileEndToEnd(t *testing.T) {
	api, _ := startRealtimeStack(t)
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "vault_websocket_connections 8\nvault_event_queue_pending 0\n")
	}))
	t.Cleanup(metrics.Close)

	profile, err := chatload.DefaultProfile()
	require.NoError(t, err)
	profile.Users, profile.GroupSize = 8, 4
	profile.SendRate, profile.ReadRate, profile.RetryShare = 4, 2, 0.25
	profile.WarmupSeconds, profile.Series, profile.SeriesSeconds = 0, 1, 10
	profile.Reconnect = chatload.ReconnectProfile{Users: 2, OfflineSeconds: 3, Seconds: 13}
	profile.Ramp = chatload.RampProfile{}
	// Latency under the race detector says nothing about the stand.
	profile.Gates.SendP95Ms, profile.Gates.SendP99Ms = 10000, 10000
	profile.Gates.ReadP95Ms, profile.Gates.ReadP99Ms = 10000, 10000
	profile.Gates.DeliveryP95Ms, profile.Gates.DeliveryP99Ms = 10000, 10000
	profile.Gates.MaxP95Spread, profile.Gates.MaxReconnectMs = 100, 10000
	invitations := make([]chatload.Invitation, profile.Users)
	for i := range invitations {
		email := fmt.Sprintf("load%04d-%d@example.com", i, time.Now().UnixNano())
		invitations[i] = chatload.Invitation{Email: email, Token: testInvitation(t, email)}
	}
	control := &snapshots{}
	runner, err := chatload.NewRunner(chatload.Config{
		Profile: profile, GatewayURL: strings.TrimSuffix(api, "/api/v1"),
		Metrics:     map[string]string{"auth": metrics.URL, "chat": metrics.URL, "gateway": metrics.URL},
		Invitations: invitations, Control: control, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := runner.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"series-1/start", "series-1/end", "reconnect/start", "reconnect/end"}, control.marks)
	require.Empty(t, report.Violations)
	require.True(t, report.Passed)

	require.Equal(t, 8, report.Setup.Connections)
	series := report.Series[0]
	require.Equal(t, int64(40), series.Sends)
	require.Equal(t, series.Sends, series.Acknowledged)
	require.Positive(t, series.Retries)
	require.Equal(t, int64(20), series.Reads)
	require.Equal(t, series.ExpectedDeliveries, series.Deliveries)
	require.Positive(t, series.Deliveries)
	require.Zero(t, series.LiveDuplicates)
	require.Zero(t, series.Integrity.Violations())
	require.Equal(t, 6, series.Integrity.Rooms)
	require.Equal(t, 40, series.Integrity.Messages)

	reconnect := report.Reconnect
	require.Equal(t, 2, reconnect.Reconnect.Reconnected)
	require.Positive(t, reconnect.Reconnect.Recovered, "the offline clients missed messages")
	require.Zero(t, reconnect.Integrity.Violations())
	require.Equal(t, 40+52, reconnect.Integrity.Messages)
}
