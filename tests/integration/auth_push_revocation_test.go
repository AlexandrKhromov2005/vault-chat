package integration

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/push"
	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/repository"
	"github.com/AlexandrKhromov2005/vault-chat/internal/shared/eventbus"
)

// interceptingProvider records messages and runs during, a change committed
// while the provider request for the matching token is in progress.
type interceptingProvider struct {
	mu     sync.Mutex
	sent   []push.Message
	during map[string]func()
}

func (p *interceptingProvider) Send(_ context.Context, m push.Message) error {
	p.mu.Lock()
	p.sent = append(p.sent, m)
	during := p.during[m.Token]
	p.mu.Unlock()
	if during != nil {
		during()
	}
	return nil
}

func (p *interceptingProvider) messages() []push.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]push.Message(nil), p.sent...)
}

// TestPushDispatch_NoSendStartsAfterRevocation: the dispatcher loads a user's
// tokens once; a revocation or a token move committed while it is sending to
// an earlier device must stop the send to that device from starting.
func TestPushDispatch_NoSendStartsAfterRevocation(t *testing.T) {
	changes := map[string]func(pool *pgxpool.Pool, sessions *repository.SessionRepository, userID, loginID string) error{
		"logout": func(_ *pgxpool.Pool, sessions *repository.SessionRepository, userID, loginID string) error {
			return sessions.RevokeFamily(context.Background(), userID, loginID)
		},
		"revoke all": func(_ *pgxpool.Pool, sessions *repository.SessionRepository, userID, _ string) error {
			_, err := sessions.RevokeAllForUser(context.Background(), userID)
			return err
		},
		"token moved to another user": func(pool *pgxpool.Pool, sessions *repository.SessionRepository, _, _ string) error {
			ctx := context.Background()
			other := newTestUser()
			if err := repository.NewUserRepository(pool).Create(ctx, other); err != nil {
				return err
			}
			login := newTestSession(other.ID, time.Hour)
			if err := sessions.Create(ctx, login); err != nil {
				return err
			}
			return sessions.RegisterPushToken(ctx, other.ID, login.ID, "fcm", "queued-device")
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			pool, sessions, user := newSessionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// PushTargets lists the most recently registered first, so
			// first-device is sent before queued-device.
			queued, first := newLogin(t, sessions, user.ID), newLogin(t, sessions, user.ID)
			require.NoError(t, sessions.RegisterPushToken(ctx, user.ID, queued.ID, "fcm", "queued-device"))
			require.NoError(t, sessions.RegisterPushToken(ctx, user.ID, first.ID, "fcm", "first-device"))
			// A second user marks the end of the first user's send: with one
			// worker, pairs are sent one after another in signal order.
			marker := newTestUser()
			require.NoError(t, repository.NewUserRepository(pool).Create(ctx, marker))
			markerLogin := newLogin(t, sessions, marker.ID)
			require.NoError(t, sessions.RegisterPushToken(ctx, marker.ID, markerLogin.ID, "fcm", "marker-device"))

			changed := make(chan error, 1)
			provider := &interceptingProvider{during: map[string]func(){
				"first-device": func() { changed <- change(pool, sessions, user.ID, queued.ID) },
			}}
			dispatcher := push.NewDispatcher(provider, sessions, slog.New(slog.NewTextHandler(io.Discard, nil)),
				push.Config{Window: time.Minute, Workers: 1})
			done := make(chan struct{})
			go func() { dispatcher.Run(ctx); close(done) }()
			t.Cleanup(func() { cancel(); <-done })

			room := uuid.NewString()
			dispatcher.Signal(eventbus.PushSignal{RoomID: room, UserIDs: []string{user.ID, marker.ID}})
			require.Eventually(t, func() bool {
				sent := provider.messages()
				return len(sent) > 0 && sent[len(sent)-1].Token == "marker-device"
			}, 10*time.Second, 10*time.Millisecond)
			require.NoError(t, <-changed)
			require.Equal(t, []push.Message{{Token: "first-device", RoomID: room}, {Token: "marker-device", RoomID: room}},
				provider.messages(), "no send starts for a device whose login was revoked or which left the user")
		})
	}
}
