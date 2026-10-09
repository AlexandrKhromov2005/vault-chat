# Chat room foundation

This increment defines `chat.v1.ChatService`, Chat's PostgreSQL schema and the
room input validation. Storage and the internal room service follow in dependent
increments. This contract does not expose a running gRPC server or REST routes.
Messages, room listing, member removal, ownership transfer and public discovery
are subsequent increments.

## Identity boundary

Service methods accept a trusted actor ID. Before exposing these methods, the
transport must validate an access token through Auth and derive the actor from
that response. Never accept the actor from a client field or an unverified header.
The transport must also resolve target users through an Auth directory API before
creating a dialog or adding a channel member. That directory API is not yet
implemented. UUID validation alone cannot establish account existence.

Chat owns its own database. User IDs are opaque external references, with no
foreign keys or queries to Auth's tables. Database credentials must restrict each
service to its own database. Apply `migrations.ChatFS` with directory `chat` to the
Chat database, never to the Auth database.

## Room rules

- A direct dialog contains two distinct nonzero user UUIDs and is always private.
  Both participants have equal rights; there is no owner and no operation for
  adding a third participant. Repeated creation in either order returns the same
  room, including concurrent creation.
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
- The repository implementation must check membership and management rights itself; a transport
  cannot authorize a write using a separate, potentially stale membership read.

## Verification

Unit and fuzz tests do not need infrastructure. The storage increment adds PostgreSQL integration tests using
`VAULT_CHAT_TEST_CHAT_DATABASE_URL`, a separate disposable Chat database. They
truncate Chat tables; do not point this variable at production data. Auth tests
continue using `VAULT_CHAT_TEST_DATABASE_URL`.

```sh
go test -race ./internal/chat/...
VAULT_CHAT_TEST_CHAT_DATABASE_URL='postgres://review:review@localhost:55435/chat_review?sslmode=disable' \
  go test -race ./tests/integration -run '^TestChat'
```

The migration creates a unique sorted UUID pair for direct dialogs and a
membership primary key. The repository will expose only enrollment operations that create
both direct participants, create the channel owner, or add members as an owner.
There is no arbitrary membership write exposed by the service.
