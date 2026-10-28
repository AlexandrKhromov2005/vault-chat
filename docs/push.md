# Push notifications

A push is only a hint that a client should synchronize. Messages are always
fetched over the authenticated API, so a lost, delayed or forged push never
loses or reveals a message. The Android client registers a device token per
login; Auth stores it and later sends wake-up signals through a push
provider (FCM first).

This document covers device tokens (this change) and the planned dispatch
(next change), so that both are reviewed against the same invariants.

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

## Dispatch (next change)

Decisions recorded now; implemented separately.

- **Source.** Chat already publishes committed events to Redis. For each new
  message it also publishes a minimal signal (room id and recipients without
  the sender, no text, names or sender) on one channel. Auth subscribes to that
  channel only, so it never receives message content.
- **Sender in Auth.** Auth owns the tokens and session state, so tokens never
  leave Auth except towards the provider, and deleting tokens the provider
  rejects is a local transaction. No new inter-service RPC is needed.
- **Payload.** A data-only message `{"type":"sync","room_id":"…"}`, high
  priority, collapse key per room. No notification text: the client fetches
  and renders it.
- **Collapsing.** Per user, at most one push per room in a window (default
  10 seconds); signals arriving in the window are merged into one push after
  it. Memory and queue are bounded; overflow drops signals, never blocks Chat.
- **Open WebSocket.** Push is sent anyway. Presence is per user and per
  Gateway instance, while devices are separate: a browser tab must not mute
  the phone. The client ignores the signal while its own socket is connected.
- **Provider.** Interface `PushProvider`; FCM HTTP v1 behind it, configured
  by a service account file outside the repository and disabled by default.
  Tests use a stub; no real keys or Firebase files exist in the repository.
- **Invalid tokens.** Provider answers meaning "unregistered" or "invalid
  token" delete exactly that token; other errors are counted and dropped.
