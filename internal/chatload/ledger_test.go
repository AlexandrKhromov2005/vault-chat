package chatload

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func message(sequence int64, sender string) Message {
	const room = "r"
	return Message{
		ID: fmt.Sprintf("m-%s-%d", room, sequence), RoomID: room, SenderID: sender,
		ClientID: fmt.Sprintf("c-%s-%d", room, sequence), Text: "text", Sequence: sequence,
	}
}

func TestLedgerMeasuresDeliveryFromTheStartOfTheSend(t *testing.T) {
	ledger := NewLedger()
	phase := NewPhaseStats()
	ledger.SetPhase(phase)
	start := time.Unix(100, 0)
	m := message(1, "alice")
	ledger.Sending(m.ClientID, "r", phase, start)
	// The event can overtake the REST answer.
	ledger.Live("bob", m, start.Add(30*time.Millisecond))
	ledger.Acknowledged(m.ClientID, m)
	ledger.Live("alice", m, start.Add(50*time.Millisecond))

	summary := phase.Delivery.Summary()
	require.Equal(t, 2, summary.Count)
	require.InDelta(t, 50, summary.Max, 1e-9)
	require.Equal(t, int64(2), phase.Deliveries())
	require.Equal(t, int64(0), phase.LiveDuplicates())
	require.Equal(t, int64(1), ledger.Known("bob", "r"))
}

func TestLedgerCountsOnlyRepeatedLiveEventsAsDuplicates(t *testing.T) {
	ledger := NewLedger()
	phase := NewPhaseStats()
	ledger.SetPhase(phase)
	first, second := message(1, "alice"), message(2, "alice")

	ledger.Live("bob", first, time.Now())
	ledger.Live("bob", first, time.Now())
	require.Equal(t, int64(1), phase.LiveDuplicates(), "the same event delivered twice")

	// Catch-up and a late live event overlap by design.
	require.True(t, ledger.CatchUp("bob", second))
	require.False(t, ledger.CatchUp("bob", second))
	ledger.Live("bob", second, time.Now())
	require.Equal(t, int64(1), phase.LiveDuplicates())
	require.Equal(t, int64(2), ledger.Known("bob", "r"))
}

func TestLedgerKnownSequenceStopsAtTheFirstGap(t *testing.T) {
	ledger := NewLedger()
	ledger.SetPhase(NewPhaseStats())
	ledger.Live("bob", message(1, "alice"), time.Now())
	ledger.Live("bob", message(3, "alice"), time.Now())
	require.Equal(t, int64(1), ledger.Known("bob", "r"))
	ledger.CatchUp("bob", message(2, "alice"))
	require.Equal(t, int64(3), ledger.Known("bob", "r"))
	require.Equal(t, int64(0), ledger.Known("carol", "r"))
}

func TestLedgerVerifyAcceptsACompleteRoom(t *testing.T) {
	ledger := NewLedger()
	ledger.SetPhase(NewPhaseStats())
	history := []Message{message(1, "alice"), message(2, "bob")}
	for _, m := range history {
		ledger.Sending(m.ClientID, "r", nil, time.Now())
		ledger.Acknowledged(m.ClientID, m)
		ledger.Acknowledged(m.ClientID, m) // an identical retry
		for _, user := range []string{"alice", "bob"} {
			ledger.Live(user, m, time.Now())
		}
	}
	integrity := ledger.Verify("r", []string{"alice", "bob"}, history)
	require.Equal(t, Integrity{Rooms: 1, Messages: 2}, integrity)
	require.Zero(t, integrity.Violations())
}

func TestLedgerVerifyFindsEveryKindOfViolation(t *testing.T) {
	ledger := NewLedger()
	ledger.SetPhase(NewPhaseStats())
	one, two, three := message(1, "alice"), message(2, "alice"), message(4, "alice")
	lost := message(9, "alice")
	retried := message(2, "alice")
	retried.ID = "another"
	stored := message(5, "alice")
	stored.ClientID = one.ClientID
	for _, m := range []Message{one, two, three, lost} {
		ledger.Sending(m.ClientID, "r", nil, time.Now())
		ledger.Acknowledged(m.ClientID, m)
	}
	ledger.Acknowledged(two.ClientID, retried) // a retry answered by a new message
	ledger.Live("bob", one, time.Now())
	conflicting := message(2, "alice")
	conflicting.ID = "not-two"
	ledger.Live("bob", conflicting, time.Now())

	integrity := ledger.Verify("r", []string{"bob"}, []Message{one, two, three, stored})
	require.Equal(t, 1, integrity.SequenceGaps, "3 is missing")
	require.Equal(t, 1, integrity.MissingAcknowledged, "the lost message")
	require.Equal(t, 2, integrity.StoredDuplicates, "the retry and the second message of one client id")
	require.Equal(t, 1, integrity.ViewConflicts)
	require.Equal(t, 2, integrity.ViewMissing, "4 and 5")
	require.Equal(t, 7, integrity.Violations())
}

func TestIntegrityAdds(t *testing.T) {
	a := Integrity{Rooms: 1, Messages: 2, StoredDuplicates: 1, SequenceGaps: 2, MissingAcknowledged: 3, ViewMissing: 4, ViewConflicts: 5}
	require.Equal(t, Integrity{Rooms: 2, Messages: 4, StoredDuplicates: 2, SequenceGaps: 4, MissingAcknowledged: 6, ViewMissing: 8, ViewConflicts: 10}, a.Add(a))
}
