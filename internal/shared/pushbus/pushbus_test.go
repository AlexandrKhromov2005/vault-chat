package pushbus_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/pushbus/pushbustest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// connect returns a connection of service and the permission errors the
// broker reports to it.
func connect(t *testing.T, srv *pushbustest.Server, service string) (*nats.Conn, <-chan error) {
	t.Helper()
	tlsConfig := pushbustest.TLS(t, service, srv.CA.Issue(t, service))
	errs := make(chan error, 16)
	conn, err := pushbus.Connect(srv.URL(), service, tlsConfig, quiet, nats.RetryOnFailedConnect(false),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case errs <- err:
			default:
			}
		}))
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	return conn, errs
}

func jetStream(t *testing.T, conn *nats.Conn) jetstream.JetStream {
	t.Helper()
	js, err := jetstream.New(conn)
	require.NoError(t, err)
	return js
}

func short(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func requirePermissionViolation(t *testing.T, errs <-chan error) {
	t.Helper()
	select {
	case err := <-errs:
		require.ErrorContains(t, err, "Permissions Violation")
	case <-time.After(2 * time.Second):
		t.Fatal("expected a permissions violation")
	}
}

// Each service holds only its own part of the stream: Chat publishes, Auth
// declares and consumes. ADR 0003, acceptance test 12.
func TestBrokerPermissionsSeparateChatAndAuth(t *testing.T) {
	srv := pushbustest.Start(t)
	authConn, authErrs := connect(t, srv, "auth")
	chatConn, chatErrs := connect(t, srv, "chat")
	ctx := context.Background()

	consumer, err := pushbus.Declare(ctx, jetStream(t, authConn))
	require.NoError(t, err)
	publisher, err := pushbus.NewPublisher(chatConn)
	require.NoError(t, err)
	lost, err := publisher.Publish(ctx, []pushbus.Signal{{ID: pushbus.SignalID("m", 0), Payload: []byte(`{}`)}})
	require.NoError(t, err)
	require.Zero(t, lost)
	msg, err := consumer.Next(jetstream.FetchMaxWait(2 * time.Second))
	require.NoError(t, err)
	require.NoError(t, msg.Ack())

	t.Run("chat cannot read or manage the stream", func(t *testing.T) {
		chat := jetStream(t, chatConn)
		_, err := chat.Stream(short(t), pushbus.StreamName)
		require.Error(t, err)
		requirePermissionViolation(t, chatErrs)
		_, err = chat.Consumer(short(t), pushbus.StreamName, pushbus.ConsumerName)
		require.Error(t, err)
		requirePermissionViolation(t, chatErrs)
		_, err = chatConn.SubscribeSync(pushbus.Subject)
		require.NoError(t, err) // refused asynchronously
		requirePermissionViolation(t, chatErrs)
		_, err = chatConn.SubscribeSync("_INBOX_auth.>")
		require.NoError(t, err)
		requirePermissionViolation(t, chatErrs)
	})

	t.Run("auth cannot publish, delete or purge", func(t *testing.T) {
		auth := jetStream(t, authConn)
		_, err := auth.Publish(short(t), pushbus.Subject, []byte(`{}`))
		require.Error(t, err)
		requirePermissionViolation(t, authErrs)
		require.Error(t, auth.DeleteStream(short(t), pushbus.StreamName))
		requirePermissionViolation(t, authErrs)
		stream, err := auth.Stream(short(t), pushbus.StreamName)
		require.NoError(t, err)
		require.Error(t, stream.Purge(short(t)))
		requirePermissionViolation(t, authErrs)
		_, err = auth.CreateOrUpdateConsumer(short(t), pushbus.StreamName, jetstream.ConsumerConfig{Durable: "other"})
		require.Error(t, err)
		requirePermissionViolation(t, authErrs)
	})
}

// Only Chat's and Auth's certificates from the deployment CA get a session:
// other services of the same CA, a foreign CA and a client without a
// certificate are refused.
func TestBrokerRefusesOtherIdentities(t *testing.T) {
	srv := pushbustest.Start(t)
	for _, service := range []string{"gateway", "media", "preview"} {
		tlsConfig := pushbustest.TLS(t, service, srv.CA.Issue(t, service))
		_, err := pushbus.Connect(srv.URL(), service, tlsConfig, quiet, nats.RetryOnFailedConnect(false))
		require.ErrorIs(t, err, nats.ErrAuthorization, service)
	}

	foreign := pushbustest.TLS(t, "chat", pushbustest.NewAuthority(t).Issue(t, "chat"))
	_, err := pushbus.Connect(srv.URL(), "chat", foreign, quiet, nats.RetryOnFailedConnect(false))
	require.Error(t, err)

	chat := pushbustest.TLS(t, "chat", srv.CA.Issue(t, "chat"))
	tlsConfig, err := chat.ClientTLS("nats")
	require.NoError(t, err)
	tlsConfig.Certificates = nil
	_, err = nats.Connect(srv.URL(), nats.Secure(tlsConfig), nats.Timeout(2*time.Second))
	require.Error(t, err)
}

// The stream is bounded and keeps unread signals: when full it refuses new
// ones. ADR 0003, acceptance test 13 (the broker side).
func TestStreamConfigIsBounded(t *testing.T) {
	cfg := pushbus.StreamConfig()
	require.Equal(t, jetstream.WorkQueuePolicy, cfg.Retention)
	require.Equal(t, jetstream.DiscardNew, cfg.Discard)
	require.Equal(t, jetstream.FileStorage, cfg.Storage)
	require.Equal(t, time.Hour, cfg.MaxAge)
	require.Positive(t, cfg.MaxMsgs)
	require.Positive(t, cfg.MaxBytes)
	require.True(t, cfg.DenyDelete && cfg.DenyPurge)
	consumer := pushbus.ConsumerConfig()
	require.Equal(t, jetstream.AckExplicitPolicy, consumer.AckPolicy)
	require.Equal(t, 20, consumer.MaxAckPending)
	require.Equal(t, 5, consumer.MaxDeliver)
	require.Empty(t, consumer.BackOff)
}

func TestSignalIDIsOpaqueAndStable(t *testing.T) {
	id := pushbus.SignalID("0b5a4f8e-6c8b-4ad2-9c1e-2f0d1f0a1b2c", 0)
	require.True(t, pushbus.ValidSignalID(id))
	require.Equal(t, id, pushbus.SignalID("0b5a4f8e-6c8b-4ad2-9c1e-2f0d1f0a1b2c", 0))
	require.NotEqual(t, id, pushbus.SignalID("0b5a4f8e-6c8b-4ad2-9c1e-2f0d1f0a1b2c", 1))
	require.NotContains(t, id, "0b5a4f8e")
	for _, bad := range []string{"", "x", id[:63], id + "0", "G" + id[1:], "A" + id[1:]} {
		require.False(t, pushbus.ValidSignalID(bad), bad)
	}
}
