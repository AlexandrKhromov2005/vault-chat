package chatload

import (
	"sync"
	"sync/atomic"
	"time"
)

// Message is the REST and WebSocket form of a chat message.
type Message struct {
	ID        string    `json:"message_id"`
	RoomID    string    `json:"room_id"`
	SenderID  string    `json:"sender_id"`
	ClientID  string    `json:"client_message_id"`
	Text      string    `json:"text"`
	Sequence  int64     `json:"sequence"`
	CreatedAt time.Time `json:"created_at"`
}

// PhaseStats accumulates the client-side measurements of one phase.
type PhaseStats struct {
	Send, Read, Delivery Latencies

	sends, acknowledged, retries, reads  atomic.Int64
	deliveries, expected, liveDuplicates atomic.Int64
	inFlight, maxInFlight                atomic.Int64

	mu       sync.Mutex
	errors   map[string]int64
	closures map[string]int64
}

// NewPhaseStats returns empty statistics.
func NewPhaseStats() *PhaseStats {
	return &PhaseStats{errors: map[string]int64{}, closures: map[string]int64{}}
}

// Deliveries is the number of live events received for messages of the phase.
func (s *PhaseStats) Deliveries() int64 { return s.deliveries.Load() }

// LiveDuplicates is the number of live events a client had already received live.
func (s *PhaseStats) LiveDuplicates() int64 { return s.liveDuplicates.Load() }

func (s *PhaseStats) fail(kind string) {
	s.mu.Lock()
	s.errors[kind]++
	s.mu.Unlock()
}

func (s *PhaseStats) closed(code string) {
	s.mu.Lock()
	s.closures[code]++
	s.mu.Unlock()
}

func (s *PhaseStats) begin() {
	n := s.inFlight.Add(1)
	for {
		peak := s.maxInFlight.Load()
		if n <= peak || s.maxInFlight.CompareAndSwap(peak, n) {
			return
		}
	}
}

func (s *PhaseStats) end() { s.inFlight.Add(-1) }

type send struct {
	room  string
	phase *PhaseStats
	start time.Time
	// acknowledged holds the distinct message ids the API answered with.
	acknowledged []string
}

type entry struct {
	id   string
	live bool
}

type view struct {
	entries map[int64]entry
	known   int64
}

type viewKey struct{ user, room string }

// Ledger tracks every message the generator sent and what every client has
// received of every room, live or through catch-up. It is safe for
// concurrent use.
type Ledger struct {
	phase atomic.Pointer[PhaseStats]

	mu    sync.Mutex
	sends map[string]*send
	views map[viewKey]*view
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{sends: map[string]*send{}, views: map[viewKey]*view{}}
}

// SetPhase selects where live duplicates are counted from now on.
func (l *Ledger) SetPhase(phase *PhaseStats) { l.phase.Store(phase) }

// Sending records that the generator starts sending clientID to room at the
// given time; deliveries of the message are measured from it and counted in
// phase, if any.
func (l *Ledger) Sending(clientID, room string, phase *PhaseStats, at time.Time) {
	l.mu.Lock()
	if _, ok := l.sends[clientID]; !ok {
		l.sends[clientID] = &send{room: room, phase: phase, start: at}
	}
	l.mu.Unlock()
}

// Acknowledged records a successful answer to a send of clientID.
func (l *Ledger) Acknowledged(clientID string, m Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.sends[clientID]
	if !ok {
		s = &send{room: m.RoomID}
		l.sends[clientID] = s
	}
	for _, id := range s.acknowledged {
		if id == m.ID {
			return
		}
	}
	s.acknowledged = append(s.acknowledged, m.ID)
}

// Live applies a message.created event that user received at the given time.
func (l *Ledger) Live(user string, m Message, at time.Time) {
	l.mu.Lock()
	s := l.sends[m.ClientID]
	v := l.view(user, m.RoomID)
	old, seen := v.entries[m.Sequence]
	switch {
	case !seen:
		v.add(m.Sequence, entry{id: m.ID, live: true})
	case old.id == m.ID:
		v.entries[m.Sequence] = entry{id: m.ID, live: true}
	}
	// A different message at a known sequence stays out of the view;
	// Verify reports whichever of the two the history does not have.
	l.mu.Unlock()

	if seen && old.live && old.id == m.ID {
		if phase := l.phase.Load(); phase != nil {
			phase.liveDuplicates.Add(1)
		}
		return
	}
	if s != nil && s.phase != nil {
		s.phase.deliveries.Add(1)
		s.phase.Delivery.Add(at.Sub(s.start))
	}
}

// CatchUp applies a message read through catch-up and reports whether the
// client did not have it yet.
func (l *Ledger) CatchUp(user string, m Message) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.view(user, m.RoomID)
	if _, seen := v.entries[m.Sequence]; seen {
		return false
	}
	v.add(m.Sequence, entry{id: m.ID})
	return true
}

// Known is the sequence up to which user has every message of room.
func (l *Ledger) Known(user, room string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if v, ok := l.views[viewKey{user, room}]; ok {
		return v.known
	}
	return 0
}

func (l *Ledger) view(user, room string) *view {
	key := viewKey{user, room}
	v, ok := l.views[key]
	if !ok {
		v = &view{entries: map[int64]entry{}}
		l.views[key] = v
	}
	return v
}

func (v *view) add(sequence int64, e entry) {
	v.entries[sequence] = e
	for {
		if _, ok := v.entries[v.known+1]; !ok {
			return
		}
		v.known++
	}
}

// Integrity counts what is wrong with the stored history of rooms and with
// what clients have of it. Every field but Rooms and Messages is a violation.
type Integrity struct {
	Rooms    int `json:"rooms"`
	Messages int `json:"messages"`
	// StoredDuplicates counts a client message id stored more than once in a
	// room, or answered with different messages.
	StoredDuplicates int `json:"stored_duplicates"`
	// SequenceGaps counts sequence numbers missing from the history.
	SequenceGaps int `json:"sequence_gaps"`
	// MissingAcknowledged counts acknowledged sends absent from the history.
	MissingAcknowledged int `json:"missing_acknowledged"`
	// ViewMissing counts (member, message) pairs a client lacks after catch-up.
	ViewMissing int `json:"view_missing"`
	// ViewConflicts counts sequences at which a client has another message.
	ViewConflicts int `json:"view_conflicts"`
}

// Violations is the number of problems found.
func (i Integrity) Violations() int {
	return i.StoredDuplicates + i.SequenceGaps + i.MissingAcknowledged + i.ViewMissing + i.ViewConflicts
}

// Add sums two results.
func (i Integrity) Add(o Integrity) Integrity {
	return Integrity{
		Rooms: i.Rooms + o.Rooms, Messages: i.Messages + o.Messages,
		StoredDuplicates: i.StoredDuplicates + o.StoredDuplicates, SequenceGaps: i.SequenceGaps + o.SequenceGaps,
		MissingAcknowledged: i.MissingAcknowledged + o.MissingAcknowledged,
		ViewMissing:         i.ViewMissing + o.ViewMissing, ViewConflicts: i.ViewConflicts + o.ViewConflicts,
	}
}

// Verify compares the complete history of room, oldest first, with every
// acknowledged send to it and with the views of its members.
func (l *Ledger) Verify(room string, members []string, history []Message) Integrity {
	result := Integrity{Rooms: 1, Messages: len(history)}
	stored := map[string]bool{}
	clients := map[[2]string]bool{}
	var previous int64
	for _, m := range history {
		if m.Sequence > previous+1 {
			result.SequenceGaps += int(m.Sequence - previous - 1)
		}
		previous = max(previous, m.Sequence)
		key := [2]string{m.SenderID, m.ClientID}
		if clients[key] || stored[m.ID] {
			result.StoredDuplicates++
		}
		clients[key] = true
		stored[m.ID] = true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.sends {
		if s.room != room || len(s.acknowledged) == 0 {
			continue
		}
		if !stored[s.acknowledged[0]] {
			result.MissingAcknowledged++
		}
		result.StoredDuplicates += len(s.acknowledged) - 1
	}
	for _, member := range members {
		v := l.views[viewKey{member, room}]
		for _, m := range history {
			var e entry
			var ok bool
			if v != nil {
				e, ok = v.entries[m.Sequence]
			}
			switch {
			case !ok:
				result.ViewMissing++
			case e.id != m.ID:
				result.ViewConflicts++
			}
		}
	}
	return result
}
