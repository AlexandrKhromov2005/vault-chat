package domain

// EventKind names a committed change announced to the users entitled to see it.
type EventKind string

const (
	// MessageCreated announces a newly stored message.
	MessageCreated EventKind = "message.created"
	// MemberAdded announces a new membership, including a room's initial members.
	MemberAdded EventKind = "member.added"
	// MemberRemoved announces that the owner removed a member.
	MemberRemoved EventKind = "member.removed"
	// MemberLeft announces that a member left the room.
	MemberLeft EventKind = "member.left"
)

// Event is a committed change and its recipients, read in the transaction that
// made it: the room's members at the commit and, when a membership ended, the
// former member.
type Event struct {
	Kind   EventKind
	RoomID string
	// UserID is the member a membership event is about.
	UserID string
	// Message is the stored message of a MessageCreated event.
	Message    *Message
	Recipients []string
}
