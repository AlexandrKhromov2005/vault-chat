package pushbus

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

// answeredFuture is a publication whose answer has already arrived.
type answeredFuture struct {
	ok  chan *jetstream.PubAck
	err chan error
	msg *nats.Msg
}

func (f *answeredFuture) Ok() <-chan *jetstream.PubAck { return f.ok }
func (f *answeredFuture) Err() <-chan error            { return f.err }
func (f *answeredFuture) Msg() *nats.Msg               { return f.msg }

// answered acknowledges every signal at once, or fails the ones in fail.
type answered struct{ fail map[string]bool }

func (a answered) PublishMsgAsync(msg *nats.Msg, _ ...jetstream.PublishOpt) (jetstream.PubAckFuture, error) {
	f := &answeredFuture{ok: make(chan *jetstream.PubAck, 1), err: make(chan error, 1), msg: msg}
	if a.fail[string(msg.Data)] {
		f.err <- errors.New("maximum messages exceeded")
	} else {
		f.ok <- &jetstream.PubAck{Stream: StreamName}
	}
	return f, nil
}

// A signal whose PubAck arrived is stored even when the publish timeout
// ended before Publish looked at it: counting it lost would make the
// metric and the warning lie.
func TestPublishCountsAnswersThatArrivedBeforeTheTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Publisher{js: answered{fail: map[string]bool{"refused": true}}}
	signals := []Signal{{ID: SignalID("refused", 0), Payload: []byte("refused")}}
	for i := range 200 {
		signals = append(signals, Signal{ID: SignalID("m", i), Payload: []byte("stored")})
	}
	for range 20 {
		lost, err := p.Publish(ctx, signals)
		require.Equal(t, 1, lost, "only the refused signal is lost")
		require.ErrorIs(t, err, ErrNotStored)
		require.ErrorContains(t, err, "maximum messages exceeded")
	}
}

// Only the form SignalID produces is valid: 64 lower-case hex digits.
func FuzzValidSignalID(f *testing.F) {
	for _, id := range []string{"", SignalID("m", 0), "0123", "G" + SignalID("m", 0)[1:]} {
		f.Add(id)
	}
	f.Fuzz(func(t *testing.T, id string) {
		want := len(id) == 64
		for _, c := range id {
			want = want && (c >= '0' && c <= '9' || c >= 'a' && c <= 'f')
		}
		if ValidSignalID(id) != want {
			t.Fatalf("ValidSignalID(%q) = %v", id, !want)
		}
		if !ValidSignalID(SignalID(id, len(id))) {
			t.Fatalf("SignalID of %q is not valid", id)
		}
	})
}
