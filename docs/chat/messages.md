# Messages and attachments

Chat stores immutable text messages in its own database. Both gRPC methods
authenticate through Auth and check room membership in PostgreSQL. Gateway
forwards the verified access token; a caller cannot supply a sender ID.
Absent rooms and rooms the caller has not joined both return `404` / `NOT_FOUND`,
including when their history is empty. Public channels still require invitation.

## Send and retry

`POST /api/v1/rooms/{room_id}/messages` requires an access token and JSON:

```json
{"client_message_id":"cccf3cdd-53a1-45cd-aa8e-f2b4a8c3e293","text":"Hello\nworld"}
```

The client generates a nonzero UUID once and keeps it for retries. The server
returns `200` with `message_id`, `room_id`, `sender_id`, `client_message_id`,
`text`, `sequence` and `created_at`. A retry by the same sender in the same room returns
the original ID and timestamp. Changing the text under that key returns `409`
(`ALREADY_EXISTS` in gRPC); another sender or room has a separate key namespace.
Membership is checked again on every retry.

Text must be valid UTF-8, contain non-whitespace content and be at most 4096
Unicode code points / 16384 bytes. Newline and tab are allowed; other Unicode
control characters are rejected. Text is preserved without trimming or Unicode
normalization. Clients must render it as text rather than executable HTML.

## Attachments

An optional `attachment_ids` array accepts up to five distinct Media file UUIDs.
Each file must be complete, uploaded by the sender and belong to the same room.
Text may be empty with attachments; otherwise the text rules above apply.
Retries compare both text and the ordered attachment list. References are
included in REST, history, catch-up and WebSocket messages; empty arrays are
omitted from JSON. See [Media](../media/files.md) for upload and download routes.

## History

`GET /api/v1/rooms/{room_id}/messages?limit=50` returns:

```json
{"messages":[],"next_cursor":""}
```

Messages are newest first. Request the next older page with the returned
`next_cursor` in `cursor`. The default limit is 50, explicit limits are 1–100.
An empty next cursor means that the page reached the end at the time of the read.
Duplicate query parameters, unknown parameters and noncanonical cursors/limits
return `400`. The gRPC equivalent is `ListMessages`; a zero limit selects 50.

Cursors are positive decimal sequence numbers local to a room, exclusive of the
last returned message. They are positions, not credentials or a snapshot token.
New sends after the first page do not displace older pages; fetch the first page
again to see new messages. Changing rooms starts a separate history.

## Catch-up after a position

`sequence` numbers the committed messages of a room 1, 2, 3, ... without
gaps: a failed write rolls its number back and a retry reuses the original
message. A client that has seen sequence `n` fetches what it missed with:

```
GET /api/v1/rooms/{room_id}/messages?after=n&limit=100
```

The page holds messages with a greater sequence, oldest first. A nonempty
`next_cursor` is the `after` value of the next page; an empty one means the
client has reached the newest message at the time of the read. `after=0`
starts at the first message. `after` must be `0` or a canonical positive
decimal and cannot be combined with `cursor`, even an empty one. Membership is
checked on every page, so a removed member gets `404` like a nonmember. The
gRPC equivalent is the `after` field of `ListMessages`; the Chat service
rejects a noncanonical `after` ("invalid catch-up position") and a nonempty
`cursor` together with `after` with `INVALID_ARGUMENT`. Gateway additionally
rejects a `cursor` parameter next to `after` even when it is empty, which gRPC
cannot distinguish from an absent cursor.

## Consistency

Sends take a room row lock and commit the message and sequence together.
This prevents a cursor from skipping a lower-sequence message whose transaction
has not yet committed. Failed writes roll back the sequence; retries do not
advance it. Each history page checks membership and reads messages in one SQL
snapshot. The unique `(room_id, sequence)` index supports history pagination.
The room lock serializes sends to one room; this is a deliberate initial
throughput tradeoff, not a guarantee of large-channel scalability.

There are no attachments, edits or deletion yet. A new message is announced
through Redis Pub/Sub and delivered over WebSocket, see
[Real-time events](realtime.md); no NATS event is published.
