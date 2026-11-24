//go:build compose

package compose

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
)

// The broker tests run against the push overlay's container, driven by
// tests/compose/push_broker_acceptance.py in two phases around a restart of
// the container: store, then keep.
const (
	natsURLEnv   = "VAULT_CHAT_COMPOSE_NATS_URL"
	natsCertsEnv = "VAULT_CHAT_COMPOSE_RPC_CERTS"
	pushPhaseEnv = "VAULT_CHAT_COMPOSE_PUSH_PHASE"
)

// acceptanceSignal is the id of the signal stored before the restart.
var acceptanceSignal = pushbus.SignalID("push broker acceptance", 0)

func brokerConn(t *testing.T, service string) (*nats.Conn, error) {
	t.Helper()
	t.Setenv("RPC_TLS_MODE", "mtls")
	t.Setenv("RPC_TLS_DIR", filepath.Join(os.Getenv(natsCertsEnv), service))
	tlsConfig, err := rpctls.Load(service)
	require.NoError(t, err)
	return pushbus.Connect(os.Getenv(natsURLEnv), service, tlsConfig, slog.New(slog.NewTextHandler(io.Discard, nil)),
		nats.RetryOnFailedConnect(false))
}

func requirePhase(t *testing.T, phase string) {
	t.Helper()
	if os.Getenv(natsURLEnv) == "" || os.Getenv(pushPhaseEnv) != phase {
		t.Skipf("%s and %s=%s are not set", natsURLEnv, pushPhaseEnv, phase)
	}
}

// Auth declares the stream, Chat stores a signal, and no other identity of
// the deployment CA gets a session.
func TestPushBrokerStoresSignalsFromChatOnly(t *testing.T) {
	requirePhase(t, "store")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	auth, err := brokerConn(t, "auth")
	require.NoError(t, err)
	defer auth.Close()
	js, err := jetstream.New(auth)
	require.NoError(t, err)
	_, err = pushbus.Declare(ctx, js)
	require.NoError(t, err)

	chat, err := brokerConn(t, "chat")
	require.NoError(t, err)
	defer chat.Close()
	publisher, err := pushbus.NewPublisher(chat)
	require.NoError(t, err)
	payloads, err := eventbus.EncodePushSignals("6f0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d", []string{"0a1b2c3d-4e5f-4a6b-9c7d-8e9fa0b1c2d3"})
	require.NoError(t, err)
	lost, err := publisher.Publish(ctx, []pushbus.Signal{{ID: acceptanceSignal, Payload: payloads[0]}})
	require.NoError(t, err)
	require.Zero(t, lost)

	for _, service := range []string{"gateway", "media", "preview"} {
		_, err := brokerConn(t, service)
		require.ErrorIs(t, err, nats.ErrAuthorization, service)
	}
}

// After the container restarted, the signal is still in the stream on the
// broker's volume and is delivered to Auth.
func TestPushBrokerKeepsSignalsAcrossRestart(t *testing.T) {
	requirePhase(t, "keep")
	auth, err := brokerConn(t, "auth")
	require.NoError(t, err)
	defer auth.Close()
	js, err := jetstream.New(auth)
	require.NoError(t, err)
	consumer, err := js.Consumer(context.Background(), pushbus.StreamName, pushbus.ConsumerName)
	require.NoError(t, err)
	msg, err := consumer.Next(jetstream.FetchMaxWait(5 * time.Second))
	require.NoError(t, err)
	require.Equal(t, acceptanceSignal, msg.Headers().Get(jetstream.MsgIDHeader))
	_, err = eventbus.DecodePushSignal(msg.Data())
	require.NoError(t, err)
	require.NoError(t, msg.DoubleAck(context.Background()))
}
