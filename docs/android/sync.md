# Android client: rooms, history and live events

Design of stage 3 of the [plan](plan.md): the client reads rooms and messages
into a local database, keeps it current over WebSocket and catch-up, and
shows the connection state. Sending is stage 4. The protocol is described in
[Real-time events](../chat/realtime.md) and [Messages](../chat/messages.md).

## Components

| Component | Role |
|---|---|
| `ChatDatabase` (Room) | Rooms, messages and public profiles of one signed-in user on one server. The UI reads only from it |
| `Timeline` | Pure rules that decide how a page or a live message changes a room's stored block of messages |
| `ChatSync` | REST reads (room list, latest page, catch-up, older pages, profiles) and live events, written to the database |
| `Realtime` | The WebSocket: authentication, re-authentication, close codes, reconnection with backoff |
| `ChatSession` | Everything above for one session; runs the WebSocket while the app is in the foreground and a network is available |

## Trust boundaries

- **Gateway responses and WebSocket frames** are input from the network.
  Every page is checked before it is stored: room and user IDs are UUIDs,
  every message belongs to the requested room, sequences are positive and
  consecutive in the direction of the page, catch-up starts right after the
  requested position, text is at most 4096 characters, at most 5 attachment
  IDs. A page that fails the check is not stored; a catch-up page that fails
  it replaces the room's block with the latest page. Unknown event types and
  fields are ignored; a malformed frame is dropped. A `ready` frame for
  another user closes the connection.
- **Session binding.** Every request and every WebSocket authentication is
  bound to the `SignedIn` state it was made for. After a logout or a sign-in
  as someone else the request fails instead of using the new user's token,
  so one user's data never reaches another user's database.
- **Local storage.** One database file per server and user
  (`chat-<hash>.db`), in the app's private storage, excluded from backup and
  device transfer like all app data. It is not encrypted beyond Android's
  file-based encryption **[решить]**: SQLCipher would protect a rooted or
  forensically imaged device, at the cost of a native dependency and a key
  in the Keystore. Logout and session expiry clear the tables at once and
  delete the file when no screen uses it any more; files of other sessions
  are deleted at start-up.

## Stored state per room

Messages of a room are numbered 1, 2, 3, ... without gaps. The database holds
one contiguous block of them, `oldest..newest`:

- `newest` is null until a page has been read; then it is the highest
  sequence up to which every message is stored, the `after` position for
  catch-up. An empty room has `newest = 0`.
- `oldest` is the lowest stored sequence; older history exists while
  `oldest > 1` and is read with `cursor = oldest`.

A live message with `sequence = newest + 1` extends the block; a lower one is
a duplicate; a higher one is a gap: it is not stored, and the room catches up
after `newest`, which returns it too. A latest page that does not reach the
block replaces it. Times are stored in microseconds, the server's precision:
two messages can share a millisecond.

Unread counts are local: messages of others after the newest one seen on
this device, which is when the newest message is on screen. History present
when a room is first loaded by a sync or by opening it counts as read; a room
loaded because of a live message shows that message as unread. In a dialog
that someone has just opened with the user every message is unread; in a
channel the user was added to, history from before counts as read. When unread
messages lie before the stored block, the count is shown as a lower bound
("10+").

## Synchronisation

A full sync runs when the app comes to the foreground with a network, after
every WebSocket `ready` that opens a connection (not after re-authentication),
on pull-to-refresh and after a rate limit has passed. Requests coalesce: one
sync at a time, and a request during a sync runs one more after it.

1. Read the room list page by page. If it took more than one page, read the
   first page again: rooms with new activity move to the top and would
   otherwise be missed. Store the rooms; delete rooms that are not listed,
   with their messages, only if the listing is complete.
2. Catch up every loaded room whose `last_message_at` is newer than its newest
   stored message.
3. Read the latest page of the most recently active rooms that have never
   been loaded, so that the list shows their last message. Other rooms are
   loaded when opened.
4. Read unknown public profiles: dialog peers and senders.

Membership changes of the room list (step 1 and `member.*` events for the
user) are serialised, so that a room added by an event during a listing that
started before it is not deleted as unlisted. Re-reading the first page
covers up to one page of rooms that moved during the listing; if more did,
some are deleted as unlisted, and a room the user was removed from during
the listing may be stored again from an earlier page. The next sync or event
corrects both.

Live events that need a room refreshed (a gap, a room not loaded yet) are
coalesced per room: events during a refresh add one more refresh after it.
A room that an event adds is read from the server outside the event loop and
outside the room-list lock, which is taken only to store it, so waiting for
the budget never holds up live events. A room the user left while it was
being read is not stored; one who joins again meanwhile gets it after all.

Opening a room loads or catches it up at once, without waiting for
background work on the same room: the database keeps the block contiguous
whichever answer arrives first, so the lock that background work takes only
saves duplicate requests.

## Resource budgets

| Budget | Value | Why |
|---|---|---|
| Gateway rate limit per user and IP | 120/min, burst 30 (server default) | Sync must stay well inside it |
| Background requests (listing, preload, catch-up, profiles) | 60/min, burst 15 (token bucket) | Half of the Gateway's limit stays for the user |
| Profiles | Only while more than 5 tokens are left | Listing, catch-up and joined rooms never wait behind names |
| Requests the user waits for (opening a room, older history) | Not delayed; they draw on the same bucket, at most 15 into debt | Background work waits them out |
| Concurrent sync requests | 2 | Leaves room for the user's own requests |
| Rooms a sync loads or catches up at once | 2 | The others hold no lock while they wait, so opening any of them is not delayed |
| Room list | 100 per page, at most 50 pages | 5000 rooms; beyond that the list is stored but nothing is deleted |
| Rooms preloaded per sync | 10 | Bounds the first sync of a new device |
| Catch-up | 100 per page, at most 10 pages per room | After 1000 missed messages the room is reset to its latest page |
| History page | 50 | One screen and a half |
| Response size | 1 MiB (existing limit) | |
| Profiles | one request per unknown user, cached for the session | Gateway has no batch profile endpoint |
| WebSocket reconnect | 0.5 s doubling to 30 s, with jitter; `Retry-After` honoured | |

A `429` pauses all sync requests until its `Retry-After` has passed, then
the sync runs again. On the first sync of an account with many dialogs the
names of their peers arrive over a minute or more, at the background rate.

## Failure behaviour

| Failure | Behaviour |
|---|---|
| No network | Shown as "Нет сети"; nothing is attempted until a network appears |
| Network changes | The WebSocket is reopened at once and a sync follows its `ready` |
| Request fails (network, 5xx, timeout) | The sync stops and is shown; the next trigger retries. Stored data stays visible |
| `401` | The access token is renewed once (SessionManager); a rejected refresh signs out |
| `404` for a room | The user lost access: the room and its messages are deleted |
| WebSocket 4001 | Renew the token, reconnect at once; a second 4001 in a row reconnects after the backoff |
| Re-authentication | Before the token has 60 s left, and at least 11 s after the last `ready` (the Gateway requires 10 s since it sent `ready`) |
| WebSocket 1001, 1013, 4002, lost connection | Reconnect with backoff, then sync |
| WebSocket 1003, 1008, 1009 | Client bug: stop reconnecting until the next foreground or network change |
| No `ready` within 15 s of opening | Treated as a lost connection |
| Dead connection without close | OkHttp pings every 30 s and drops the socket when no pong arrives |
| App goes to the background | The WebSocket closes; nothing runs in the background until stage 7 |
| Logout or session end during a sync | Results for the ended session are dropped; its database is cleared |

## Acceptance tests

JVM tests, against MockWebServer and a fake WebSocket where the network is
involved:

- Timeline rules: extend, duplicate, gap, replace on a distant latest page,
  older pages only when contiguous, malformed pages rejected.
- Sync: a listing that loses a room deletes it; a truncated listing deletes
  nothing; a room moved to the top during a listing is kept; a room added by
  an event during a listing survives it; 404 deletes the room; 429 pauses
  and retries after `Retry-After`; a catch-up longer than the budget resets
  the room; results of an ended session are not stored.
- Realtime: auth frame first; re-authentication before expiry, never less
  than 10 s apart; 4001 renews and reconnects at once; backoff grows and
  resets after `ready`; client-error codes stop; a missing `ready` times out;
  a `ready` for another user closes the connection.
- Session binding: a request made for one session is not sent with the token
  of the next.
- Database: the stored block stays contiguous through the DAO transactions
  (Robolectric).

Not covered by automated tests yet: Compose screens, and the lifecycle and
network observers, which are checked by hand on a device.
