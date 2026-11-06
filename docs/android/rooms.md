# Android client: dialogs, channels and members

Design of stage 5 of the [plan](plan.md): the user finds other users by
name, opens a dialog with one, creates a channel, sees who is in a channel,
and, as its owner, adds and removes members; a member can leave. The server
side is described in [Rooms](../chat/rooms.md) and
[gateway.yaml](../api/gateway.yaml). Reading and live events are in
[sync.md](sync.md), sending in [send.md](send.md).

## Components

| Component | Role |
|---|---|
| `Membership` | User search, opening a dialog, creating a channel, a channel's roster, adding and removing members, leaving |
| `ChatSync` | Stores the rooms that `Membership` creates or leaves under the room-list lock; applies `member.*` events of other users to stored rosters; reloads the rosters on screen after each full sync |
| `ChatDatabase` v5 | Adds `members` (room, user, join time) and `rosters` (whether a room's stored roster is complete). Both go with their room |
| `UserSearch` | The search field's logic: validation, debounce, the newest query wins |
| Screens | New chat (search and "create channel"), create channel, members (with "add member", which reuses the search) |

The UI still reads only from the database, except search results, which
are shown as they arrive and are not a source of truth.

## Trust boundaries

- **Gateway responses are input from the network**, like in sync.md. They
  are checked before anything is stored:
  - a search page has at most the requested number of users, every ID is a
    UUID and appears once, and the caller is not among them (the server
    excludes them; the client drops them anyway);
  - a dialog returned by `POST /rooms/direct` is `direct`, and its
    `participant_ids` are exactly the caller and the chosen user;
  - a channel returned by `POST /rooms/channels` is a `channel` owned by the
    caller with the requested name and privacy;
  - a member page has at most the requested number of members, user IDs are
    lowercase canonical UUIDs in strictly ascending order and after the
    cursor, and a non-empty `next_cursor` is the last ID of the page.

  A response that fails the check is reported as an invalid response and
  nothing is stored. A room the server returns for a create or open request
  is stored only after this check, so a broken response cannot add a room
  that the user never asked for.
- **Input of the user** is checked with the server's rules before a request
  is sent, to give an answer without a round trip; the server checks again.
  A search query is trimmed and must match `^[A-Za-z][A-Za-z0-9_-]{1,31}$`;
  anything else sends nothing. A channel name is trimmed and must have 1–80
  code points without control characters.
- **Rights.** The client shows owner actions only to the owner and "leave"
  only to other members of a channel, but the server decides; a `403` or
  `409` is shown as an error and the roster is read again.
- **Session binding** is unchanged: every request goes through the
  `ChatSession` it was made for (sync.md).
- **Names** are shown as plain text; nothing from the server is interpreted
  as markup or a link.

## Rooms the user creates, opens or leaves

A room returned by opening a dialog or creating a channel is stored at the
top of the list under the room-list lock (sync.md), so a listing that began
before it existed cannot delete it as unlisted. If a listing is under way,
storing waits for it. A dialog that is already stored keeps its messages.
The new room is then opened like any other: its latest page is loaded and
its history counts as read.

Leaving deletes the room with its messages and unsent queue, like
`member.left` for the user, and counts as a departure, so that a room read
for an earlier event is not stored again. The confirmation says how many
unsent messages will be deleted with it (the decision of 2026-10-28 in
send.md).

## Rosters

A channel's roster is read when its members screen opens: `GET
/rooms/{room_id}/members` page by page, 100 per page, at most 10 pages. The
pages replace the stored roster in one transaction; with more than 1000
members the stored roster is marked incomplete and the screen says that only
the first 1000 are shown. Members are ordered by user ID, so the order does
not depend on names that are still loading.

While stored, a roster follows `member.added`, `member.removed` and
`member.left` for other users, and the client's own successful add and
remove. Events can be lost while the WebSocket is down, so every full sync
reads the rosters of members screens that are open again; closed screens
read theirs on the next opening. A sync that ends while a roster is being
read makes the screen read it once more afterwards, since the read under
way may predate what the sync recovered. A dialog has no roster screen: its
two participants are part of the room.

Names of members are read only for the rows on screen once the list has
stood still for 300 ms, at the profile priority of sync.md. Scrolling on or
closing the screen cancels the requests that still wait for the budget.

## Resource budgets

| Budget | Value | Why |
|---|---|---|
| Search | 300 ms after the last key, one request at a time, a new query cancels the old one | Typing a name costs a few requests, not one per key |
| Search page | 20 users | One screen; the user narrows the prefix for more |
| Search, dialog, channel, member changes | User priority of sync.md: no wait, at most 15 into debt | The user waits for them |
| One action at a time per screen | The button is disabled while its request runs | No double creation by a double tap |
| Roster | 100 per page, at most 10 pages per load; the first at user priority, the rest at background priority | A huge channel does not exhaust the budget |
| Roster reloads | On opening the screen and after a full sync while it is open; events are applied without a request | A busy channel does not cause a request per event |
| Member names | Rows on screen after 300 ms without scrolling, profile priority, cancelled with the screen | A fling through a thousand members costs one screen of names |

All requests share the 429 pause of sync and sending.

## Failure behaviour

| Failure | Behaviour |
|---|---|
| Search fails (network, 5xx, timeout) | The error is shown below the field; the next change of the query tries again |
| `429` | "Слишком много запросов" with the wait; every request pauses until `Retry-After` |
| Opening a dialog fails | Shown; nothing stored. The request is idempotent on the server, so trying again is safe |
| `404` when opening a dialog | The user no longer exists |
| Creating a channel: no answer, 5xx, timeout or an invalid answer | The request is not idempotent, so its outcome is unknown: the client does not repeat it, OkHttp is not allowed to repeat it either (a one-shot body), the form keeps the name, a sync is started, and the message says to check the room list before trying again |
| Creating a channel: the answer cannot be stored | The channel exists on the server: reported as an unknown outcome, and the sync it starts stores the channel |
| Creating a channel: `400` | The name is rejected; nothing was created |
| `401` | The token is renewed and the request repeated once (SessionManager); a `401` means that the Gateway did not pass it on, so this is safe for creation too |
| Adding a member: `404` | Either the user or the room is gone: the room is read again; a `404` for it deletes the room, otherwise the user is reported as not found |
| Removing a member: `404` | The caller lost access: the room is deleted and the screen closes |
| Leaving: `204` or `404` | Either way the user is no longer a member: the room is deleted and the app returns to the room list. A repeat after a lost answer gets `404`, which is the same outcome |
| `403`, `409` | Shown; the roster is read again, since the rights the screen assumed were wrong |
| Roster fails (network, 5xx) | The stored roster stays on screen with the error and "Повторить" |
| Roster `404` | The room is deleted and the screen closes |
| Room deleted while one of its screens is open (event, sync) | The members screen and the conversation close |
| Database failure in any of these actions | Reported as "could not store on this device"; nothing crashes |
| Session ends | Results for the ended session are dropped, as in sync.md |

## Acceptance tests

JVM and Robolectric tests against the fake Gateway:

- Search: queries that break the server's rules send nothing; only the
  newest query's answer is shown; a page with a non-UUID ID or over its
  limit is rejected; the caller is dropped.
- Dialog: a dialog is stored at the top and kept with its messages when it
  already existed; a response with other participants is rejected and
  stores nothing; a dialog opened during a listing survives the listing.
- Channel: a created channel is stored; a response with another owner or
  name stores nothing; the request body is one-shot so OkHttp cannot repeat
  it (MockWebServer); a lost answer starts a sync and is reported as
  unknown.
- Roster: pages replace the stored roster; malformed pages (order, cursor,
  size) store nothing; more than 10 pages mark it incomplete; events of other
  users add and remove members; a `404` deletes the room.
- Changes: add and remove update the roster; a `404` on add with the room
  still readable reports the user, and one with the room gone deletes it;
  leaving deletes the room and its queue on `204` and on `404`, and a room
  read for an earlier `member.added` is not stored again.
- Database: the v5 tables cascade with their room (Robolectric).

Not covered by automated tests: the Compose screens, which are checked by
hand on a device.

## Review of 42d1914

Every finding below failed its test on 42d1914 before its fix. The names
test failed there in the reviewer's form, against the former
`wantProfiles` (300 requests for a fling through 300 rows); it now drives
the flow of visible rows that replaced it.

| Finding | Test | Resolution |
|---|---|---|
| A failure other than an `ApiException` crashed the screen, including a database failure after the server had created the channel, which also skipped the sync and the "check the list" message | `aStorageFailureAfterCreatingIsAnUnknownOutcome` | A failure to store a created channel is an unknown outcome; the screens report any other failure as a local one |
| A sync that ended while the roster was read was dropped, so a change missed while offline stayed missing until the next sync | `aSyncThatEndsDuringARosterReadIsNotDropped` | Reloads during a read run one more read after it |
| Names were requested for every row composed while scrolling, in the session's scope: a fling through 1000 members cost 1000 requests, and closing the screen did not stop them | `namesOfRowsScrolledPastAreNotRequested` | Rows on screen after 300 ms, requested in the screen's coroutine |
| A search page with a user twice crashed the list, whose keys are user IDs | `aSearchPageWithAUserTwiceIsRejected` | Rejected as an invalid response |
| The same member in upper and lower case passed the order check and was stored twice | `aMalformedRosterStoresNothing` | Member IDs must be lowercase, as the server sends them |

Not changed:

- **`forRoom` deletes a room on `404` without counting a departure**, now
  also for rosters and adding members. A room read for an earlier event
  may come back, and the next request for it gets `404` and deletes it
  again. Inherited from stage 3 and self-correcting.
- **Channel names may contain format characters** such as U+202E, which the
  server accepts. They are shown as plain text, as all names are; whether
  to restrict them is for the server, where every client would benefit.

## Review of 627bcdf

The fixes of the first round were confirmed. Both regressions failed on
627bcdf before their fix.

| Finding | Test | Resolution |
|---|---|---|
| A name request cancelled before it started never released its in-flight mark, so that name was never read again in the session | `aNameWhoseRequestWasCancelledBeforeItStartedIsReadLater` | The request always starts and releases its mark; a cancelled one sends nothing. The background path shares it |
| Reading names moved into the screen, where a database failure escaped and crashed the app | `aDatabaseFailureWhileReadingNamesDoesNotEscape` | Failures stay inside; the name is read when the rows on screen change or the screen opens again |

Not changed: a name the screen marked in flight is skipped by a background
read at the same moment; if the screen then scrolls on and cancels it, the
background caller does not ask again until its next call (the next sync,
opening the room, or the row on screen again).

The third round, on df8e174, confirmed both fixes and found nothing new.
