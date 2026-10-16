# Real-time events

Chat announces every committed change that clients show live. Gateway
instances relay these events to their clients over WebSocket. A client that
was disconnected recovers what it missed through catch-up.

## Publishing

After a transaction commits, Chat publishes a protobuf `chat.v1.Event`
(`api/proto/chat/v1/events.proto`) to the Redis Pub/Sub channel
`chat:user:<user_id>` of each recipient. The channel names live in
`internal/shared/eventbus`, so Chat and Gateway agree on them without
importing each other:

| Kind | Published when | Recipients |
|---|---|---|
| `message_created` | A new message is stored; retries publish nothing | Room members at the commit, sender included |
| `member_added` | A channel is created, a dialog is created, or the owner adds a user who was not a member | All members, the new one included |
| `member_removed` | The owner removes a member; removing a nonmember publishes nothing | Remaining members and the removed user |
| `member_left` | A member leaves a channel | Remaining members and the user who left |

Recipients are read in the transaction that makes the change, while the room
row is locked. Sends and membership changes take that lock first, so the
recipients are exactly the members at the commit: a member removed before a
send never receives it, and a removed user still learns of the removal.
Gateway does not filter by room; it relays a user's channel only to that
user's own connections.

Publishing is best effort and never delays a request. After the commit the
request only puts the change on an in-memory queue of
`CHAT_EVENT_QUEUE_SIZE` (1024) changes and returns. One worker per Chat
process publishes the queue in order, all events of a change in one pipeline,
each round trip bounded by `CHAT_EVENT_PUBLISH_TIMEOUT` (1s). A Redis failure
is logged and the worker moves on, because the change has already committed.
While Redis is down or slow the queue fills up; further changes are then
dropped, with a rate-limited warning that carries the running total. On
shutdown Chat stops serving requests first and then publishes what is queued
for at most `CHAT_EVENT_DRAIN_TIMEOUT` (5s); the rest is dropped.

Events can therefore be lost. A single Chat process publishes changes in the
order they were queued, but concurrent requests may queue them in a different
order than they committed, and several Chat processes publish independently.
Clients detect a missing message by its `sequence` and fetch it with
catch-up, described in [Messages](messages.md#catch-up-after-a-position).

Each recipient costs one publish. This keeps membership filtering exact at
the cost of fan-out for large channels; per-room channels with Gateway-side
membership checks are the alternative if that cost matters.

## WebSocket connection

`GET /api/v1/ws` upgrades to a WebSocket (RFC 6455) without compression. The
upgrade counts against the per-IP rate limit like any API request, but the
connection is not subject to the request timeout.

Browsers send an `Origin` header; it must be the Gateway's own origin or one of
`GATEWAY_CORS_ALLOWED_ORIGINS` (`*` allows any), otherwise the upgrade gets
`403 origin_forbidden`. Clients without `Origin` are accepted, since the
connection authenticates with a bearer token, not a cookie.

Each Gateway instance holds at most `GATEWAY_WS_MAX_CONNECTIONS` (10000)
connections, of which at most `GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS`
(1000, never more than the total) may still be waiting for their first valid
`auth` frame. A connection counts against the second limit from the upgrade
until its authentication succeeds or fails. When either limit is reached the
upgrade is refused with `503 too_many_connections` and `Retry-After`, before
any WebSocket is opened. These limits do not depend on Redis, unlike the
per-IP rate limit, which lets requests through while Redis is down. During
shutdown upgrades get `503 shutting_down`.

### Authentication

Browsers cannot set an `Authorization` header on a WebSocket, and URLs end up
in logs, so the token is never taken from the URL. Within
`GATEWAY_WS_AUTH_TIMEOUT` (5s) the client sends one text frame:

```json
{"type":"auth","token":"<access token>"}
```

The Gateway validates it through Auth, like a protected REST request,
subscribes to the user's events and answers:

```json
{"type":"ready","user_id":"...","expires_at":"2026-10-15T12:15:00Z"}
```

Every event that reaches the Gateway after `ready` is delivered or the
connection is closed, so the client catches up only after `ready`. Publishing
itself remains best effort, so sequences still matter, see below.

The connection is closed with 4001 when the access token expires. To keep it
open, the client refreshes the token over REST and sends a new `auth` frame
with the new access token before `expires_at`. The token must belong to the
same user; the Gateway answers with a new `ready` frame. Auth frames sent
less than 10 seconds apart close the connection with 1008. Logout and session
revocation do not end a connection earlier, just as an issued access token
stays valid for REST until it expires.

### Events

Every event is a JSON text frame with `type` and `room_id`. Clients must ignore
unknown types and fields.

```json
{"type":"message.created","room_id":"...","message":{"message_id":"...","room_id":"...","sender_id":"...","client_message_id":"...","text":"...","sequence":7,"created_at":"..."}}
{"type":"member.added","room_id":"...","user_id":"..."}
{"type":"member.removed","room_id":"...","user_id":"..."}
{"type":"member.left","room_id":"...","user_id":"..."}
```

`message` has exactly the REST message form. A membership event whose
`user_id` is the client's own user means that the room was added to or removed
from the user's list; `member.added` carries no room metadata, which the
client reads with `GET /api/v1/rooms/{room_id}`.

### Limits and close codes

| Limit | Default | Setting |
|---|---|---|
| Connections per Gateway instance | 10000 | `GATEWAY_WS_MAX_CONNECTIONS` |
| Unauthenticated connections per Gateway instance | 1000 | `GATEWAY_WS_MAX_UNAUTHENTICATED_CONNECTIONS` |
| Connections per user and Gateway instance | 5 | `GATEWAY_WS_MAX_CONNECTIONS_PER_USER` |
| Events queued per connection | 64 | `GATEWAY_WS_SEND_BUFFER` |
| Time to authenticate; also bounds token checks | 5s | `GATEWAY_WS_AUTH_TIMEOUT` |
| Server ping interval | 30s | `GATEWAY_WS_PING_INTERVAL` |
| Write and pong deadline | 10s | `GATEWAY_WS_WRITE_TIMEOUT` |
| Client frame size | 4096 bytes | fixed |

The client only sends `auth` frames and must answer pings, which browsers do
automatically.

| Code | Meaning | Client action |
|---|---|---|
| 1001 | The Gateway instance is shutting down | Reconnect, then catch up |
| 1003 | A binary frame was sent | Fix the client |
| 1008 | Malformed frame, unknown frame type or re-authentication too frequent | Fix the client |
| 1009 | Frame over 4096 bytes | Fix the client |
| 1013 | Per-user connection limit reached, Auth or the event stream unavailable, or events may have been lost upstream | Reconnect with backoff, then catch up |
| 4001 | Authentication missing, invalid, expired or for another user | Refresh the access token, then reconnect |
| 4002 | The client did not read its events fast enough | Reconnect, then catch up |

A connection that does not answer a ping within the write deadline is dropped
without a close frame, like any lost connection.

On SIGINT or SIGTERM the Gateway stops accepting upgrades and closes every
WebSocket connection with 1001, including connections that have not
authenticated yet. It exits only after these close handshakes have finished,
or after the shutdown timeout of 15 seconds, which also bounds draining REST
requests. A client that does not answer the close frame within that time sees
the connection drop without one.

## Delivery across Gateway instances

Each Gateway instance holds one Redis Pub/Sub connection and subscribes to
`chat:user:<user_id>` while that user has a connection on it; no state is
shared between instances, so a user may be connected to several. The
subscription is confirmed by Redis before `ready` is sent. When the Pub/Sub
connection is lost, events published meanwhile are gone: once go-redis
reconnects and resubscribes, the Gateway closes the affected users'
connections with 1013 so that their clients catch up. While Redis is down,
live delivery stops and REST keeps working.

## Reconnect and catch-up

Events are best effort: they can be lost or arrive out of order across
requests, and nothing is replayed after a reconnect. The message `sequence`
numbers each room's messages 1, 2, 3, ... without gaps, which makes losses
visible. A client keeps, per room, the highest sequence up to which it has
every message and then:

1. Connects, authenticates and waits for `ready`. Events arriving from now on
   are buffered or applied as below.
2. Reads `GET /api/v1/rooms` page by page and drops rooms that are no longer
   listed. For every room whose `last_message_at` is newer than what the
   client has, and every new room, it fetches
   `GET /api/v1/rooms/{room_id}/messages?after=<known sequence>&limit=100`
   (`after=0` for a new room) and follows `next_cursor` until it is empty.
3. Applies `message.created`: a sequence it already has is a duplicate; one
   more than the known sequence extends the room; a larger one means a gap,
   which it fills with catch-up after the known sequence.
4. On any close or lost connection, reconnects with exponential backoff and
   starts again at step 1. After 4001 it refreshes the access token first.

Duplicate messages are expected and recognized by `message_id` or by room and
`sequence`.
