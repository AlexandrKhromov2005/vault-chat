# Android client: sending messages

Design of stage 4 of the [plan](plan.md): an outgoing queue that survives
process death, idempotent retries and message states. It builds on the
local database and sync of [stage 3](sync.md). The server side is described
in [Messages](../chat/messages.md); it needs no change.

## What the server guarantees

Checked in the code, not only in the documentation:

- `POST /api/v1/rooms/{room_id}/messages` with `client_message_id` and
  `text`. The key is `(room_id, sender_id, client_id)`, a `UNIQUE`
  constraint without expiry (`migrations/chat/0002_create_messages.up.sql`):
  a retry is safe at any later time, also after a restart days later.
- A retry runs under the room's row lock, finds the message by its key and
  returns the original (same ID, sequence and time). It does not advance the
  sequence and publishes no second `message.created`. Concurrent retries
  are serialised by the lock.
- The same key with another text returns `409`. Membership is checked on
  every retry: a stored message whose sender was removed meanwhile is
  answered with `404`.
- `message.created` goes to every member at the commit, the sender
  included, and may arrive before the HTTP response.
- Errors: Chat's `DeadlineExceeded` is `504`, `Unavailable`/`Canceled`
  `503`, `ResourceExhausted` `429` (without `Retry-After`), other failures
  `500`. The Gateway's own rate limit answers `429` with `Retry-After`.

## Components

| Component | Role |
|---|---|
| `outbox` table | Messages of this user that are not yet part of a room's stored block |
| `MessageText` | The server's text rules, checked before a message is queued |
| `Outbox` | Queues messages, sends them in order, classifies answers |
| `RateGate` | One `429` pause shared by sync and sending |
| `OutboxWorker`, `sendQueuedMessages` | Send the queue with WorkManager when the app is not on screen |

## Trust boundaries

- **User input.** The composer's text is checked against the server's
  rules (`MessageText`) before it is stored, and sent unchanged: no
  trimming or normalisation, as the server stores it as given. It is shown
  as plain text, never interpreted.
- **Gateway responses.** The answer to a send must be a valid message of the
  requested room (the checks of `Pages.kt`) with this user as sender, the
  queued `client_message_id` and the queued text. Anything else is treated
  as an invalid response; the row fails, and if the message was stored after
  all, it arrives through sync and resolves the row.
- **Other users' messages.** `client_message_id` keys are per sender. A row
  is resolved only by a message with the same room, the same key **and this
  user as sender**, so another member cannot remove a queued message by
  sending one with the same key.
- **Session binding.** Requests go through `ChatSession` and are bound to its
  `SignedIn` state like the reads of stage 3. The queue lives in the
  session's database; signing out or a session end deletes it with the
  rest of the data.

## Stored state

`outbox` rows: `client_message_id` (primary key), `room_id` (deleted with the
room), `sender_id`, `position` (queue order), `text`, `created_at` (local
time in microseconds like the server's times, for display), `state`,
`sequence`, `error` and `after_sequence` (the room's newest stored sequence
when the row was queued). The table came with database version 3; without a
released version the old schema is dropped and rebuilt.

| State | Meaning | Shown as |
|---|---|---|
| `QUEUED` | Waiting or being sent | clock |
| `SENT` | Stored by the server with `sequence`, but the room's block does not reach it yet (someone else wrote meanwhile) | one tick |
| `FAILED` | Rejected by the server; not retried automatically | red "!" with Retry, Copy, Delete (other rows offer Copy) |

There is no "sending" state on disk: a send that was under way when the
process died has an unknown outcome and is simply sent again with the same
key.

**Shown exactly once.** A message is shown either from `outbox` or from
`messages`, never from both. Every transaction that writes a room's
messages (`ChatDao.write`) deletes, in the same transaction, the rows of that
room that a stored message of this user matches by key and text, and `SENT`
rows whose sequence is at most the block's newest (the message is then in the
block or in older history). This one rule covers every way a sent message
comes back: the HTTP response, the WebSocket echo, catch-up and history. The
text is compared too: a stored message with the same key and another text
(the server's `409`) shows only that this text was not stored.

Rows are shown below all stored messages of the room, in queue order. The
last row is the room's preview in the room list if it is queued or sent, or
failed with no message stored after it was queued (sequences decide, since
the device's and the server's clocks differ). Queuing a message moves its
room to the top of the list. The composer keeps the text unless it was
queued, and turns pasted `\r\n` and `\r` into `\n` as the text is typed.

**Rooms with unsent messages.** Stage 3 accepts that a listing during which
rooms move may miss one and delete it, since the next sync brings it back
with its history. The queue exists only on this device, so a complete
listing does not delete a room with unsent rows; `ChatSync` asks the server
about it, and only a `404` deletes it (with its rows). A `member.removed`
event for this user deletes the room at once, queue included.

## Sending

- Within a room strictly in order, one message at a time: if A failed for a
  moment and B were sent before A's retry, B would get the lower sequence
  for everyone. A `FAILED` row does not block the rows after it; retried, it
  goes to the end of the queue.
- Rooms are independent; at most 2 sends are under way at once, and all
  sends, repeats included, keep to a budget of 30 a minute (burst 10).
- A send starts when a message is queued, when a network appears, after the
  WebSocket's `ready`, when the app comes to the foreground and when its
  backoff has passed. The first four also cut a running backoff short.
- Nothing is sent without a network.

| Answer | Outcome | Action |
|---|---|---|
| `200` with a matching message | stored | `SENT`, then applied like a live message; a gap starts a catch-up |
| network failure, timeout, `408`, `5xx` | unknown | stays `QUEUED`; repeated with the same key after a backoff |
| `429` | not accepted | stays `QUEUED`; all sending and sync wait for `Retry-After` (30 s without one, at least 1 s, at most 5 minutes) |
| `401` | not accepted | the session renews the token and repeats once; after that like a network failure. A rejected refresh ends the session and its queue |
| `400`, `413`, `415`, invalid response | rejected | `FAILED` (`invalid`) |
| `403`, `404` | rejected | `FAILED` (`no_access`); the room is usually deleted by sync soon, and its rows with it |
| `409` | rejected | `FAILED` (`conflict`) |

**Retry** of a failed row keeps its key, except after `409`: there the key is
taken, by definition with another text, so this text was never stored and a
new key is safe. **Delete** is offered for failed rows only; a queued row may
already be stored by the server.

OkHttp may repeat a request on a stale pooled connection. For a send this is
harmless: the repeat carries the same key.

## Background

The queue matters most when the app is closed right after sending (in the
metro, phone back in the pocket). Every time a message is queued or retried,
a unique WorkManager job with a network constraint is enqueued. It replaces
a job that waits or runs (`REPLACE`): a waiting job may be in WorkManager's
backoff after retries, and a running one may already have seen an empty
queue; cancelling it is safe, since sending runs in the session's scope with
the same key. The job waits for the stored session,
asks the current `ChatSession` to send its queue and ends when no row is
`QUEUED`; after 8 minutes (WorkManager stops a job at 10), restoring the
session included, or if the database fails under it, it asks to run again
later with WorkManager's backoff. Without a session it ends at once. A
process started for the job opens no WebSocket, since the app is not in the
foreground. Sending
in the foreground and in the job is the same `Outbox` of the same process,
so a row is never sent twice at the same time. The job is not expedited:
below Android 12 that needs a foreground notification, and a job with a
network constraint already runs within minutes.

## Resource budgets

| Budget | Value | Why |
|---|---|---|
| Gateway rate limit per user and IP | 120/min, burst 30 (server default), shared with sync | Sync keeps to 60/min, burst 15, and sending to 30/min, burst 10 (two `RequestBudget`s), so that together they stay inside it even when every queued room repeats during an outage |
| Concurrent sends | 2, one per room | Rooms do not wait for each other |
| `429` pause | 1 s to 5 minutes | A `Retry-After` of 0 cannot make a busy loop, nor a bogus one stop the app for hours |
| Backoff of a room | 1 s doubling to 60 s, with jitter | Resets after a success |
| Text | 4096 code points, 16384 UTF-8 bytes | Server limits |
| Queue | Unbounded rows, each at most 16 KiB of text | Written by the user's own hand |
| Request deadline | 30 s (the app's HTTP client) | Longer than the Gateway's 10 s budget, so its answer usually arrives |
| Background job | 8 minutes per run | WorkManager stops a job after 10 |

## Failure behaviour

| Failure | Behaviour |
|---|---|
| No network | Rows stay `QUEUED` with a clock; sent when a network appears |
| Response lost after the server stored the message | Repeated with the same key; the server returns the original; one message |
| Echo before the response | The echo resolves the row; the response changes nothing |
| Process killed while sending | Next start (or the background job) sends again with the same key |
| Process killed after the commit, message already fetched by catch-up | The fetch resolved the row; nothing is sent again |
| Room deleted or access lost | `FAILED` with `no_access`; the rows go with the room once the server confirms it |
| A listing misses a room that moved meanwhile | A room with unsent rows stays until the server answers `404` for it |
| Database failure | Sending is not stopped: starting rooms is tried again after a second, and a room's sender waits a backoff and goes on, keeping its place |
| Sign-out | The confirmation names the number of unsent messages; they are deleted with the database |
| Session ended by the server | The queue is deleted with the database without asking |

## Acceptance tests

JVM tests with Robolectric (Room) and a fake Gateway that stores messages by
`(room, sender, client_id)` like the real one:

1. Lost response: the server stores, the connection breaks; the retry keeps
   the key; one message on the server and on screen.
2. Echo before the response and after it: one message, the row resolved.
3. A response with a gap: `SENT`, catch-up, then the row is resolved.
4. A queue left by a killed process is sent by a new session.
5. A message already fetched by catch-up is not sent again.
6. Order: A gets `503`, B waits behind it; `sequence(A) < sequence(B)`.
7. `429` with `Retry-After` delays the next send and sync.
8. `400`, `404`, `409` fail without automatic retries; Retry after `409` uses a
   new key, after `404` the same.
9. Another user's message with the same key does not resolve the row.
10. Text rules: 4096 code points outside the BMP pass, 4097 do not; controls
    other than newline and tab, unpaired surrogates and whitespace only are
    rejected.
11. No network: nothing is sent; a network appears: the row goes.
12. One room's `503` does not hold back another room.
13. A response that is not the queued message fails the row.
14. The background job waits for the stored session, sends its queue and
    ends; without a session it ends at once; with sends failing, or with a
    restore that does not end, it asks for another run when its budget is
    over. Queuing and retrying schedule it; a new schedule replaces a
    waiting job.
15. A room missed by a listing keeps its queue; a room the server no longer
    has takes it.
16. A database failure while starting rooms, or between two sends, does not
    stop sending.
17. Repeats in many rooms keep to the send budget; the `429` pause has a
    floor and a cap.
18. A stored message with the same key and another text does not resolve a
    failed row; a newer stored message replaces a failed row as the preview.

Not covered by automated tests: Compose screens (there is no Compose UI test
setup yet), checked by hand on a device.

## Review of 2440b66

Every finding below that has a test failed on 2440b66 before its fix.

| Finding | Test | Resolution |
|---|---|---|
| A listing that misses a room deletes its queue for good | `aRoomMissedByAListingKeepsItsQueue` | Rooms with unsent rows are deleted only after a `404` |
| A database failure in `queuedRooms` ends the collector; one in `nextQueued` leaves the room marked as being sent | `aDatabaseErrorWhileStartingRoomsDoesNotStopSending`, `aDatabaseErrorBetweenSendsDoesNotStickTheRoom` | Both caught; the room keeps its place |
| `atNewest` counted the room read while long outgoing messages hid the newest one | none (no Compose UI tests) | Visibility from the list's layout |
| `KEEP` dropped a schedule while a job ran or waited for its backoff | `queuingAgainReplacesTheWaitingJob` | `REPLACE` |
| An old failed message stayed the room's preview after a newer message | `aNewerMessageFromAPeerIsThePreviewNotAnOldFailedOne` | `after_sequence` decides |
| Repeats in many rooms could exceed the Gateway's limit | `repeatedFailuresInManyRoomsKeepToTheSendBudget` | Send budget 30/min, burst 10 |
| `Retry-After: 0` makes a busy loop, a huge one freezes the app | `RateGateTest` | Floor 1 s, cap 5 minutes |
| A stored message with the same key and another text resolved a failed row | `aConflictingStoredMessageDoesNotResolveTheFailedRow` | Text compared |
| The composer cleared text that was not queued | `aMessageForARoomThatIsGoneIsNotReportedAsQueued` | Cleared only after queuing |
| The background job's budget did not cover the restore | `theBackgroundJobKeepsToItsBudgetWhileTheSessionIsRestored` | Budget covers it; a failing database asks for another run |
| Pasted `\r` rejected without a hint; scrolling on any change of the last row; accessibility (no click label, counter not announced, failure text read twice, queued rows not copyable) | none (Compose) | Fixed as described above |

Not changed:

- **The room list order** after a sync follows the server, so a room with a
  queued message may move below rooms with older activity until the message
  is sent. Cosmetic; the next listing after sending puts it on top.
- **A frame with the message twice or not at all** while a row resolves: the
  outbox and the pager are separate streams. The database shows it once.
- **Text lost with the room.** `404` after the server confirms it, or
  `member.removed` for this user, deletes the room and its unsent rows,
  like a session that expires. Keeping text without its room needs a place
  to show it; until then this is a decision to confirm.

Checked without a defect: echo before and after the answer, lost answers,
catch-up during a send, killed processes and blocks reset to their latest
page all end with one message; the end of a room's sender racing a new
message loses no wake; another member cannot resolve this user's rows; the
queue does not leak between sessions; answers are classified as Gateway maps
gRPC codes; `MessageText` matches the Go validator.
