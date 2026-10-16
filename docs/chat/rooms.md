# Chat room foundation

This increment defines `chat.v1.ChatService`, Chat's PostgreSQL schema and the
internal room service, authenticated gRPC server (`cmd/chat`) and REST routes
through Gateway.
Member removal, ownership transfer and public discovery are subsequent
increments.

## Identity boundary

The internal service accepts a trusted actor ID. The gRPC handler requires one
`authorization: Bearer <access token>` metadata value, validates it through Auth
and derives the actor from that response. Client actor fields and user-ID headers
are not trusted. Gateway forwards the verified access token; Chat validates it
again so calling Chat directly cannot bypass authentication.

Before creating a dialog or adding a member, Chat resolves the target through
Auth's authenticated `GetUser` RPC. That API returns only ID and username; UUID
validation alone never establishes account existence. Auth failure prevents the
room operation. Request ID propagates through Gateway → Chat → Auth.

Clients find a peer's ID through Auth's `SearchUsers` RPC (Gateway
`GET /api/v1/users?query=`): a case-insensitive username prefix search that
returns the same public fields, excludes the caller, and caps a page at 50.
Chat does not call it; the chosen ID is still resolved with `GetUser`.

Chat owns its own database. User IDs are opaque external references, with no
foreign keys or queries to Auth's tables. Database credentials must restrict each
service to its own database. Apply `migrations.ChatFS` with directory `chat` to the
Chat database, never to the Auth database. `cmd/chat` applies these migrations on
startup. Compose provides a separate `chat-postgres` instance on port 5433. Chat
uses `CHAT_DATABASE_URL`; its unary requests are bounded by
`CHAT_REQUEST_TIMEOUT` (10 seconds by default), including Auth and PostgreSQL.
An earlier caller deadline takes precedence. Internal RPC is plaintext for now;
mTLS remains a separate implementation task.

## Room rules

- A direct dialog contains two distinct nonzero user UUIDs and is always private.
  Both participants have equal rights; there is no owner and no operation for
  adding a third participant. Repeated creation in either order returns the same
  room, including concurrent creation. Every operation returning a dialog reports
  both participants, including the caller, in `participant_ids` in UUID order.
  Channels leave it empty; their roster is not part of room metadata.
- A channel has one owner, enrolled atomically during creation. Names contain
  1–80 Unicode code points, without control characters or surrounding whitespace;
  they need not be unique. The owner can add members idempotently. Adding an
  existing owner does not change ownership.
- Both public and private channels currently require invitation. `private=false`
  reserves discoverability semantics for a later increment, not anonymous access.
- Reading a nonexistent room or a room without membership returns the same
  `ErrNotFound`. Management by a nonmember also returns `ErrNotFound`; an existing
  member without owner rights gets `ErrForbidden`. A direct dialog cannot be
  managed as a channel.
- The repository checks membership and management rights itself; a transport
  cannot authorize a write using a separate, potentially stale membership read.

## Room list

`GET /api/v1/rooms?limit=50` (`ListRooms` in gRPC) returns the caller's rooms:

```json
{"rooms":[{"room_id":"…","kind":"direct","private":true,"created_at":"…",
  "participant_ids":["…","…"],"last_message_at":"…"}],"next_cursor":""}
```

Each item is the room object returned by the other room routes plus
`last_message_at`, the time of the room's latest message, absent when the room
has no messages. Only rooms with the caller's membership are listed.

Rooms are ordered by activity, newest first, then by room ID descending.
Activity is the later of the latest message and the caller's own enrollment.
A channel the caller was just invited to therefore appears at the top even if
it has been quiet for a long time; for other rooms activity starts at creation.
The default limit is 50, explicit limits are 1–100. Request the next page with
the returned `next_cursor` in `cursor`; an empty next cursor means that the
page reached the end at the time of the read. Duplicate or unknown query
parameters and noncanonical cursors or limits return `400`.

Cursors are keyset positions `<activity Unix microseconds>_<room UUID>`,
exclusive of the last returned room. Clients must treat them as opaque; they are
not credentials and every page is filtered by the caller's current membership.
A room with new activity after the first page moves to the top and is not shown
again on later pages, while rooms that were not touched are neither skipped nor
repeated. Fetch the first page again to see newly active rooms.

The latest message is found through the room's message sequence and the unique
`(room_id, sequence)` index, so no activity column is maintained. Each page
sorts all of the caller's memberships; this is adequate for the expected number
of rooms per user and can be replaced by a maintained activity index later.

## Verification

Unit and fuzz tests do not need infrastructure. PostgreSQL integration tests use
`VAULT_CHAT_TEST_CHAT_DATABASE_URL`, a separate disposable Chat database. They
truncate Chat tables; do not point this variable at production data. Auth tests
continue using `VAULT_CHAT_TEST_DATABASE_URL`.

```sh
go test -race ./internal/chat/...
VAULT_CHAT_TEST_CHAT_DATABASE_URL='postgres://review:review@localhost:55435/chat_review?sslmode=disable' \
  go test -race ./tests/integration -run '^TestChat'
```

The migration creates a unique sorted UUID pair for direct dialogs and a
membership primary key. The only enrollment operations in the repository create
both direct participants, create the channel owner, or add members as an owner.
There is no arbitrary membership write exposed by the service.
