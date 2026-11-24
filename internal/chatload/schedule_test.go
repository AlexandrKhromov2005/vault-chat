package chatload

import (
	mathrand "math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func layoutRunner(t *testing.T) *Runner {
	t.Helper()
	profile, err := DefaultProfile()
	require.NoError(t, err)
	profile.Users, profile.GroupSize = 12, 4
	r := &Runner{prof: profile}
	invitations := make([]Invitation, profile.Users)
	r.layout(invitations)
	for _, u := range r.users {
		u.id = u.name
	}
	return r
}

func TestLayoutGivesEveryUserADialogAndAChannel(t *testing.T) {
	r := layoutRunner(t)
	require.Len(t, r.rooms, 6+3)
	for _, u := range r.users {
		require.Len(t, u.rooms, 2)
		require.True(t, u.rooms[0].direct)
		require.Len(t, u.rooms[0].members, 2)
		require.False(t, u.rooms[1].direct)
		require.Len(t, u.rooms[1].members, 4)
		for _, room := range u.rooms {
			require.Contains(t, room.members, u)
		}
	}
}

func TestScheduleIsRepeatableAndHasTheTargetCount(t *testing.T) {
	r := layoutRunner(t)
	draw := func(phase uint64) []operation {
		return r.schedule(mathrand.New(mathrand.NewPCG(uint64(r.prof.Seed), phase)), 40, 20, 30*time.Second)
	}
	ops := draw(1)
	require.Equal(t, ops, draw(1), "a seed and phase always give the same operations")
	require.NotEqual(t, ops, draw(2))

	var sends, reads, retries int
	ids := map[string]bool{}
	require.True(t, slices.IsSortedFunc(ops, func(a, b operation) int { return int(a.at - b.at) }))
	for _, op := range ops {
		require.GreaterOrEqual(t, op.at, time.Duration(0))
		require.Less(t, op.at, 30*time.Second)
		require.Contains(t, op.room.members, op.user)
		if op.read {
			reads++
			continue
		}
		sends++
		if op.retry {
			retries++
		}
		id, err := uuid.Parse(op.clientID)
		require.NoError(t, err)
		require.Equal(t, uuid.Version(4), id.Version())
		require.False(t, ids[op.clientID])
		ids[op.clientID] = true
		require.GreaterOrEqual(t, len(op.text), r.prof.MinTextBytes)
		require.LessOrEqual(t, len(op.text), r.prof.MaxTextBytes)
	}
	require.Equal(t, 1200, sends)
	require.Equal(t, 600, reads)
	require.InDelta(t, 0.02*1200, retries, 15)
}
