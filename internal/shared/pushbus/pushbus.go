// Package pushbus carries content-free push signals from Chat to Auth through
// NATS JetStream (docs/adr/0003-push-signals-jetstream.md). Delivery is at
// least once from the PubAck to Auth's acknowledgement; the payload is an
// eventbus.PushSignal.
package pushbus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/rpctls"
)

const (
	// StreamName is the stream of push signals; deployments/nats/nats.conf
	// grants permissions on it by name.
	StreamName = "PUSH_SIGNALS"
	// Subject is the only subject of the stream and the only one Chat may
	// publish to.
	Subject = "push.signal.v1"
	// ConsumerName is Auth's durable pull consumer.
	ConsumerName = "auth-push"
	// MaxAge is how long a signal is kept; it also bounds the FCM TTL of the
	// push it causes, counted from publication.
	MaxAge = time.Hour
	// AckWait is the time without acknowledgement or progress after which a
	// delivery is considered lost and redelivered.
	AckWait = 30 * time.Second
	// MaxDeliver bounds deliveries of one signal; Auth terminates it after
	// the last failed one.
	MaxDeliver = 5
	// MaxAckPending is how many signals Auth may hold unacknowledged. With
	// 500 recipients each, this is the 10 000 pairs of the dispatcher.
	MaxAckPending = 20
	// DuplicateWindow is how long a repeated Nats-Msg-Id is ignored.
	DuplicateWindow = 2 * time.Minute

	maxMessages = 100_000
	maxBytes    = 256 << 20
	maxMsgSize  = 32 << 10
)

// StreamConfig is the stream of push signals: on disk, removed on
// acknowledgement, at most an hour old, and refusing new signals when full
// rather than evicting unread ones.
func StreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name: StreamName, Subjects: []string{Subject}, Storage: jetstream.FileStorage, Retention: jetstream.WorkQueuePolicy,
		MaxAge: MaxAge, MaxMsgs: maxMessages, MaxBytes: maxBytes, MaxMsgSize: maxMsgSize, Discard: jetstream.DiscardNew,
		Duplicates: DuplicateWindow, Replicas: 1, DenyDelete: true, DenyPurge: true,
	}
}

// ConsumerConfig is Auth's consumer: explicit acknowledgement, no server
// back-off (Auth chooses the delays), and a bounded number of signals in
// work.
func ConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable: ConsumerName, FilterSubject: Subject, AckPolicy: jetstream.AckExplicitPolicy, AckWait: AckWait,
		MaxDeliver: MaxDeliver, MaxAckPending: MaxAckPending, MaxWaiting: 4, MaxRequestBatch: 1,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}

// Connect opens a TLS connection to url authenticated by service's internal
// certificate; the broker maps the certificate to the service's permissions.
// The connection keeps reconnecting, also when the broker is unreachable at
// start, and publishing while disconnected fails at once instead of
// buffering.
func Connect(url, service string, rpc *rpctls.Config, logger *slog.Logger, options ...nats.Option) (*nats.Conn, error) {
	tlsConfig, err := rpc.ClientTLS("nats")
	if err != nil {
		return nil, fmt.Errorf("push signal broker TLS: %w", err)
	}
	base := []nats.Option{
		nats.Name(service), nats.Secure(tlsConfig), nats.CustomInboxPrefix("_INBOX_" + service),
		nats.Timeout(2 * time.Second), nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second), nats.ReconnectJitter(500*time.Millisecond, time.Second),
		nats.ReconnectBufSize(-1), nats.PingInterval(10 * time.Second), nats.MaxPingsOutstanding(3),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Warn("push signal broker disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) { logger.Info("push signal broker reconnected") }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			logger.Warn("push signal broker error", "error", err)
		}),
	}
	conn, err := nats.Connect(url, append(base, options...)...)
	if err != nil {
		return nil, fmt.Errorf("connect push signal broker: %w", err)
	}
	return conn, nil
}

// Declare creates or updates the stream and Auth's consumer. Only Auth's
// identity is permitted to.
func Declare(ctx context.Context, js jetstream.JetStream) (jetstream.Consumer, error) {
	if _, err := js.CreateOrUpdateStream(ctx, StreamConfig()); err != nil {
		return nil, fmt.Errorf("declare push signal stream: %w", err)
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, StreamName, ConsumerConfig())
	if err != nil {
		return nil, fmt.Errorf("declare push signal consumer: %w", err)
	}
	return consumer, nil
}

// SignalID is the Nats-Msg-Id of one part of a message's signal. It is an
// opaque hash, so the broker and Auth do not learn the message id, and
// stable, so a repeated publication within DuplicateWindow is dropped.
func SignalID(messageID string, part int) string {
	sum := sha256.Sum256([]byte("vault-chat push signal v1\x00" + messageID + "\x00" + strconv.Itoa(part)))
	return hex.EncodeToString(sum[:])
}

// ValidSignalID reports whether id has the form of SignalID: anyone allowed
// to publish could have written it.
func ValidSignalID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	for _, c := range []byte(id) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Signal is one encoded push signal and its id.
type Signal struct {
	ID      string
	Payload []byte
}

// Publisher stores signals in the stream.
type Publisher struct {
	js asyncPublisher
}

// asyncPublisher is the part of jetstream.JetStream a Publisher uses.
type asyncPublisher interface {
	PublishMsgAsync(msg *nats.Msg, opts ...jetstream.PublishOpt) (jetstream.PubAckFuture, error)
}

// NewPublisher binds a connection made with Connect.
func NewPublisher(conn *nats.Conn) (*Publisher, error) {
	// Futures whose PubAck never comes (a denied publish gets no answer)
	// are released after the timeout, so they cannot accumulate.
	js, err := jetstream.New(conn, jetstream.WithPublishAsyncTimeout(5*time.Second), jetstream.WithPublishAsyncMaxPending(1024))
	if err != nil {
		return nil, fmt.Errorf("push signal publisher: %w", err)
	}
	return &Publisher{js: js}, nil
}

// ErrNotStored reports signals the stream did not acknowledge: the broker is
// unreachable, the stream is missing or full, or ctx ended first.
var ErrNotStored = errors.New("pushbus: push signals not stored")

// Publish sends signals and waits for their PubAck until ctx ends. It returns
// how many were not acknowledged, and ErrNotStored with the first cause if
// any. A signal that was stored but whose PubAck was lost counts as not
// stored; republishing it within DuplicateWindow is harmless.
func (p *Publisher) Publish(ctx context.Context, signals []Signal) (int, error) {
	futures := make([]jetstream.PubAckFuture, 0, len(signals))
	lost := 0
	var first error
	fail := func(err error) {
		lost++
		if first == nil {
			first = err
		}
	}
	for _, signal := range signals {
		msg := &nats.Msg{Subject: Subject, Data: signal.Payload, Header: nats.Header{}}
		future, err := p.js.PublishMsgAsync(msg, jetstream.WithMsgID(signal.ID), jetstream.WithExpectStream(StreamName))
		if err != nil {
			fail(err)
			continue
		}
		futures = append(futures, future)
	}
	for _, future := range futures {
		// An answer already in counts even when ctx ended meanwhile: select
		// would pick among ready cases at random.
		select {
		case <-future.Ok():
			continue
		case err := <-future.Err():
			fail(err)
			continue
		default:
		}
		select {
		case <-future.Ok():
		case err := <-future.Err():
			fail(err)
		case <-ctx.Done():
			fail(ctx.Err())
		}
	}
	if lost > 0 {
		return lost, fmt.Errorf("%w: %d of %d: %w", ErrNotStored, lost, len(signals), first)
	}
	return 0, nil
}
