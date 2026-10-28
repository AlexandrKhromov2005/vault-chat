# Push notifications

A push is only a hint that a client should synchronize. Messages are always
fetched over the authenticated API, so a lost, delayed or forged push never
loses or reveals a message. The Android client registers a device token per
login; Auth stores it and later sends wake-up signals through a push
provider (FCM first).

This document covers device tokens and their dispatch.

## Device tokens

### Model

A token belongs to one *login*: the session family that starts at login and
continues through refresh rotation (`sessions.family_id`). Table
`push_devices` in Auth's database:

| Column | Meaning |
|---|---|
| `family_id` | Login the token belongs to; primary key, references the first session of the family |
| `user_id` | Owner, for sending and per-user bounds |
| `provider` | `fcm` |
| `token` | Provider registration token, 1–4096 characters of `[A-Za-z0-9_:.-]`, unique |
| `created_at`, `updated_at` | Bookkeeping |

One token per login: registering again replaces the login's token (FCM
rotates tokens). A token registered by another login moves to the caller's
login, since a device holds one app installation and the provider token
identifies it. A user keeps at most 10 tokens; registering an eleventh drops
the least recently registered other one, never the caller's.

### API

| Route | Effect |
|---|---|
| `PUT /api/v1/push/token` `{"provider":"fcm","token":"…"}` | Bind the token to the caller's login; `204` |
| `DELETE /api/v1/push/token` | Remove the caller's login token; `204` even if there was none |

Both need a valid access token and are limited per user like the rest of
the API. Gateway forwards them to the new Auth RPCs `RegisterPushToken` and
`UnregisterPushToken`, which authorize by access token like
`RevokeAllSessions`. The access token's `sid` names a session; Auth resolves
its family in the same transaction.

### Invariants

1. A token is never stored for a login that is not active: registration
   takes the per-user session lock that every revocation takes, and checks the
   family under it with the same rule as access-token validation.
2. Revoking a login deletes its token in the same transaction. A database
   trigger on `sessions` deletes the family's token whenever a session is
   revoked without being replaced (logout, revoke-all, refresh reuse,
   password reset, email change), so no revocation path can forget it.
   Rotation sets `replaced_by` and keeps the token.
3. Logins that end without a revocation (expiry, unverified email past its
   deadline) are excluded when sending and are cleaned up on the user's next
   registration.
4. A token belongs to one login at a time (unique `token`); a user cannot read
   another user's tokens through any API.
5. Tokens are not logged; the provider is told nothing but the signal.

### Trust boundaries

- Client → Gateway → Auth: the token string is untrusted input, validated by
  Auth (provider, length, charset). Gateway only checks the JSON shape.
- Auth → provider (next change): provider answers decide token deletion.

### Resource budgets

- Body: Gateway's `GATEWAY_MAX_BODY_BYTES`; the token itself is at most
  4096 bytes.
- Rows: at most one per login and 10 per user; registration is a single
  transaction under the per-user lock already used by session changes.
- Rate: the per-user API quota; the call costs one short transaction.

### Failure behavior

- Auth database unavailable: `500` like other Auth operations, nothing is
  stored; the client retries on its next start or token rotation.
- Two users registering the same token at once: one gets `503` and may retry.
- Session revoked concurrently with registration: either the registration
  commits first and the revocation deletes the row, or the revocation commits
  first and registration answers `401`.
- Invalid input: `400` with `invalid_argument`; unknown provider likewise.

### Acceptance tests

Repository tests against PostgreSQL:

1. Register, replace and move a token between logins; 11 registrations keep
   10 tokens per user.
2. Logout, revoke-all, refresh reuse, password reset and email change (through
   the email repository) delete the login's token; rotation keeps it.
3. Registration for a revoked or expired login is refused and stores nothing;
   tokens of expired logins and of an earlier credential version are pruned.
4. A registration waiting for an uncommitted revocation of its login is
   refused once the revocation commits (deterministic, by holding the lock);
   concurrent registrations and logouts never leave a token behind.
5. The per-user bound never drops the caller's new token, even when other
   registrations committed while it waited.
6. The runtime role, without DDL, runs the revocation trigger.

Service, handler and router tests: authorization by access token, malformed
and oversized tokens, unknown providers, missing tokens, Auth errors mapped
to the documented statuses, and the routes being authenticated.

## Dispatch

### Flow

1. Chat commits a message and, in the same Redis pipeline as its user
   events, publishes a signal on `push:signal:v1`: the room id and the
   recipients other than the sender, at most 500 per signal (larger rooms are
   split). No text, names, sender or message id.
2. Auth subscribes to that channel only, so it never receives message
   content. Every signal is validated (canonical UUIDs, at most 500
   recipients, at most 24 KiB); anything else is counted and dropped.
3. The dispatcher collapses signals per user and room: the first one is sent
   at once, signals within the next 10 seconds are merged into one push at
   the end of that window, and quiet pairs are forgotten.
4. For each user it loads the tokens of active logins (same rule as access
   tokens) and sends `{"type":"sync","room_id":"…"}` as an FCM data-only
   message with high priority, collapse key `room:<id>` and a one-hour TTL.
5. Only FCM's verdict `UNREGISTERED` deletes the token that was sent. A bare
   `404` (a proxy or a wrong endpoint) and `SENDER_ID_MISMATCH` (credentials
   of another Firebase project) are configuration errors and keep the token.
   Failures are counted and dropped: push is best effort, and the next signal
   or the client's own sync recovers.

### Decisions

- **Sender in Auth.** Auth owns the tokens and session state, so tokens never
  leave Auth except towards the provider, and deleting rejected tokens is a
  local statement. No new inter-service RPC is needed.
- **Open WebSocket.** Push is sent anyway. Presence is per user and per
  Gateway instance, while devices are separate: a browser tab must not mute
  the phone. The client ignores the signal while its own socket is connected.
- **Provider.** Interface `push.Provider`; FCM HTTP v1 is implemented with the
  standard library (an RS256 service-account assertion exchanged for an access
  token, cached for its lifetime). The service account file is read once at
  start from `AUTH_PUSH_FCM_CREDENTIALS_FILE`, must be private (no access for
  others) and at most 64 KiB, and its `token_uri` must be Google's, so the
  signed assertion goes nowhere else. Without the variable push is disabled.
  Tests use stubs and synthetic keys; no Firebase project or key exists in the
  repository.

### Trust boundaries

- Redis → Auth: anyone who can publish to Redis can make Auth wake devices of
  arbitrary users. Signals carry no content, are validated and collapsed, so
  the effect is bounded to one wake-up per user and room per window. Redis
  is password-protected and on the internal events network, as for the
  session revocation hints Gateway already trusts.
- Auth → FCM: needs egress to `oauth2.googleapis.com` and
  `fcm.googleapis.com`; Google learns device tokens and room ids, never text.
- Provider answers decide deletion of exactly the token that was sent.

### Resource budgets

- At most 10 000 user/room pairs tracked; signals for new pairs beyond that
  are dropped. The send queue has the same bound and never blocks the
  subscriber, which never blocks Chat (Pub/Sub).
- Four concurrent sends; each user's token load and sends share a 10-second
  deadline; at most 10 tokens per user. HTTP answers are read up to 64 KiB.
- Shutdown stops subscribing and taking work; sends in progress finish
  within their own 10-second deadline, and Auth waits at most 15 seconds.

### Failure behavior

- Redis unavailable: Chat drops signals with its events (counted); Auth
  resubscribes every second. Signals published meanwhile are lost.
- Auth database unavailable: the send for that pair fails and is counted.
- FCM unavailable or rejecting the service account: sends fail and are
  counted; tokens are not deleted.
- Malformed signals and send failures: counted, each logged at most every
  10 seconds.

Metrics: `vault_push_pending`, `vault_push_sent_total`,
`vault_push_failures_total`, `vault_push_dropped_total`,
`vault_push_invalid_tokens_total`, `vault_push_malformed_signals_total`.

### Acceptance tests

1. A new message signals its recipients except the sender, without text;
   membership events and messages only the sender receives signal nothing.
2. Signals are validated and fuzzed; malformed, oversized, non-canonical and
   over-long recipient lists are rejected.
3. Collapsing: the first signal is sent at once, 50 in the window become one
   push after it, other rooms are separate, quiet pairs are forgotten.
4. Only rejected tokens are deleted; provider and database failures do not
   delete and do not stop later sends; the pending bound drops new pairs.
5. FCM: a data-only body with exactly the documented fields, a valid RS256
   assertion for the messaging scope, cached access tokens renewed before
   expiry, error answers mapped without leaking the token (only
   `UNREGISTERED` deletes; bare 404 and sender mismatch keep), credentials with a
   foreign `token_uri`, wrong type, project or key refused.
6. End to end with PostgreSQL and Redis: Chat's publisher to Auth's
   subscriber and dispatcher; the sender and a revoked login get nothing, a
   rejected token is deleted, an active device is woken.

### Operator configuration

Push stays disabled until an operator creates a Firebase project, stores its
service account file privately on the host, mounts it read-only into Auth,
sets `AUTH_PUSH_FCM_CREDENTIALS_FILE` and gives Auth egress to Google. A
Compose overlay and `scripts/vps.sh` support are left for the Android push
stage, together with the Firebase project; this change configures no real
project and sends no real push.
