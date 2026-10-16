package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/repository"
)

// recordingNotifier collects the events a repository announces after commits,
// one batch per change.
type recordingNotifier struct {
	mu      sync.Mutex
	batches [][]domain.Event
}

func (n *recordingNotifier) Notify(_ context.Context, events ...domain.Event) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.batches = append(n.batches, events)
}

// takeBatches returns and forgets the batches recorded so far.
func (n *recordingNotifier) takeBatches() [][]domain.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	batches := n.batches
	n.batches = nil
	return batches
}

// take returns and forgets the events recorded so far.
func (n *recordingNotifier) take() []domain.Event {
	var events []domain.Event
	for _, batch := range n.takeBatches() {
		events = append(events, batch...)
	}
	return events
}

func requireEvent(t *testing.T, event domain.Event, kind domain.EventKind, roomID, userID string, recipients ...string) {
	t.Helper()
	require.Equal(t, kind, event.Kind)
	require.Equal(t, roomID, event.RoomID)
	require.Equal(t, userID, event.UserID)
	require.ElementsMatch(t, recipients, event.Recipients)
}

func TestChatEvents_RecipientsFollowCommittedMembership(t *testing.T) {
	notifier := &recordingNotifier{}
	repo := repository.NewRoomRepository(newChatPool(t), repository.WithNotifier(notifier))
	ctx := context.Background()
	owner, member, leaver, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	channel, err := repo.CreateChannel(ctx, owner, "events", true)
	require.NoError(t, err)
	events := notifier.take()
	require.Len(t, events, 1)
	requireEvent(t, events[0], domain.MemberAdded, channel.ID, owner, owner)
	for _, user := range []string{member, leaver} {
		require.NoError(t, repo.AddChannelMember(ctx, channel.ID, owner, user))
	}
	require.NoError(t, repo.AddChannelMember(ctx, channel.ID, owner, member))
	events = notifier.take()
	require.Len(t, events, 2, "re-adding a member must not announce anything")
	requireEvent(t, events[0], domain.MemberAdded, channel.ID, member, owner, member)
	requireEvent(t, events[1], domain.MemberAdded, channel.ID, leaver, owner, member, leaver)

	key := uuid.NewString()
	message, err := repo.SendMessage(ctx, channel.ID, member, key, "hello")
	require.NoError(t, err)
	_, err = repo.SendMessage(ctx, channel.ID, member, key, "hello")
	require.NoError(t, err)
	_, err = repo.SendMessage(ctx, channel.ID, outsider, uuid.NewString(), "denied")
	require.ErrorIs(t, err, domain.ErrNotFound)
	events = notifier.take()
	require.Len(t, events, 1, "retries and rejected sends must not announce anything")
	requireEvent(t, events[0], domain.MessageCreated, channel.ID, "", owner, member, leaver)
	require.Equal(t, message, events[0].Message)

	require.NoError(t, repo.RemoveChannelMember(ctx, channel.ID, owner, member))
	require.NoError(t, repo.RemoveChannelMember(ctx, channel.ID, owner, member))
	require.NoError(t, repo.LeaveRoom(ctx, channel.ID, leaver))
	events = notifier.take()
	require.Len(t, events, 2, "an idempotent removal must not announce anything")
	requireEvent(t, events[0], domain.MemberRemoved, channel.ID, member, owner, member, leaver)
	requireEvent(t, events[1], domain.MemberLeft, channel.ID, leaver, owner, leaver)
	// Former members are no longer recipients of the very next message.
	_, err = repo.SendMessage(ctx, channel.ID, owner, uuid.NewString(), "after removal")
	require.NoError(t, err)
	events = notifier.take()
	require.Len(t, events, 1)
	requireEvent(t, events[0], domain.MessageCreated, channel.ID, "", owner)

	direct, err := repo.GetOrCreateDirect(ctx, member, outsider)
	require.NoError(t, err)
	_, err = repo.GetOrCreateDirect(ctx, outsider, member)
	require.NoError(t, err)
	batches := notifier.takeBatches()
	require.Len(t, batches, 1, "an existing dialog must not be announced again")
	events = batches[0]
	require.Len(t, events, 2, "a new dialog announces both enrollments together")
	for _, event := range events {
		require.Equal(t, domain.MemberAdded, event.Kind)
		require.Equal(t, direct.ID, event.RoomID)
		require.ElementsMatch(t, []string{member, outsider}, event.Recipients)
	}
	require.ElementsMatch(t, []string{member, outsider}, []string{events[0].UserID, events[1].UserID})
}

func TestChatEvents_FailedChangesAnnounceNothing(t *testing.T) {
	notifier := &recordingNotifier{}
	repo := repository.NewRoomRepository(newChatPool(t), repository.WithNotifier(notifier))
	ctx := context.Background()
	owner, member := uuid.NewString(), uuid.NewString()
	channel, err := repo.CreateChannel(ctx, owner, "quiet", false)
	require.NoError(t, err)
	require.NoError(t, repo.AddChannelMember(ctx, channel.ID, owner, member))
	notifier.take()
	require.ErrorIs(t, repo.AddChannelMember(ctx, channel.ID, member, uuid.NewString()), domain.ErrForbidden)
	require.ErrorIs(t, repo.RemoveChannelMember(ctx, channel.ID, owner, owner), domain.ErrOwnerMembership)
	require.ErrorIs(t, repo.LeaveRoom(ctx, channel.ID, owner), domain.ErrOwnerMembership)
	key := uuid.NewString()
	_, err = repo.SendMessage(ctx, channel.ID, member, key, "first")
	require.NoError(t, err)
	notifier.take()
	_, err = repo.SendMessage(ctx, channel.ID, member, key, "conflict")
	require.ErrorIs(t, err, domain.ErrMessageConflict)
	require.Empty(t, notifier.take())
}
