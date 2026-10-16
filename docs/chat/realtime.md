# Real-time events

Chat announces every committed change that clients show live. Gateway
instances relay these events to their connected clients.

## Publishing

After a transaction commits, Chat publishes a protobuf `chat.v1.Event`
(`api/proto/chat/v1/events.proto`) to the Redis Pub/Sub channel
`chat:user:<user_id>` of each recipient:

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
