# Text messages

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
`text` and `created_at`. A retry by the same sender in the same room returns
the original ID and timestamp. Changing the text under that key returns `409`
(`ALREADY_EXISTS` in gRPC); another sender or room has a separate key namespace.
Membership is checked again on every retry.

Text must be valid UTF-8, contain non-whitespace content and be at most 4096
Unicode code points / 16384 bytes. Newline and tab are allowed; other Unicode
control characters are rejected. Text is preserved without trimming or Unicode
normalization. Clients must render it as text rather than executable HTML.

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

Sends take a room row lock and commit the message and sequence together.
This prevents a cursor from skipping a lower-sequence message whose transaction
has not yet committed. Failed writes roll back the sequence; retries do not
advance it. Each history page checks membership and reads messages in one SQL
snapshot. The unique `(room_id, sequence)` index supports history pagination.
The room lock serializes sends to one room; this is a deliberate initial
throughput tradeoff, not a guarantee of large-channel scalability.

There are no attachments, edits, deletion or WebSocket delivery yet. History
is available by request; sending a message does not publish a NATS event.
