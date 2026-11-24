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
| `token` | Provider registration token, 1–2048 characters of `[A-Za-z0-9_:.-]`, unique |
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
- Auth → provider: provider answers decide token deletion (see Dispatch).

### Resource budgets

- Body: Gateway's `GATEWAY_MAX_BODY_BYTES`; the token itself is at most
  2048 bytes, which fits a row of the unique index even when it does not
  compress.
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

1. Chat commits a message, publishes its user events to Redis and stores a
   signal in the NATS JetStream stream `PUSH_SIGNALS` (subject
   `push.signal.v1`), waiting for the stream's acknowledgement (`PubAck`)
   within `CHAT_EVENT_PUBLISH_TIMEOUT`. A signal is the room id and the
   recipients other than the sender, at most 500 (larger rooms are split into
   parts). No text, names, sender or message id: its `Nats-Msg-Id` is an
   opaque SHA-256 of the message id and part number, so a publication
   repeated within two minutes is stored once.
2. Auth pulls from its durable consumer `auth-push` only, so it never
   receives message content. Every signal is validated (canonical UUIDs, at
   most 500 recipients, at most 24 KiB, an id of the hash form); anything
   else is terminated (`TERM`) and counted.
3. Auth looks up which recipients already have a mark for this signal (see
   below) and hands the others to the dispatcher. It fetches a signal only
   when the dispatcher has room for 500 new user/room pairs and fewer than 20
   signals are in work.
4. The dispatcher collapses signals per user and room: the first one is sent
   at once, signals within the next 10 seconds are merged into one push at
   the end of that window, and quiet pairs are forgotten. While a signal
   waits, Auth extends its acknowledgement deadline (`InProgress`) every 10
   seconds.
5. For each user it loads the tokens of active logins (same rule as access
   tokens) and sends `{"type":"sync","room_id":"…"}` as an FCM data-only
   message with high priority, collapse key `room:<id>` and a TTL of what is
   left of the newest merged signal's hour in the stream. The loaded list is
   only a set of candidates: right before each provider request the
   dispatcher checks again that the token belongs to an active login of that
   user, and skips it otherwise.
6. When the user's push was sent or got a final answer, Auth writes a mark
   `(signal id, user id)` for every signal merged into it, valid for two
   hours. A signal is acknowledged when all its recipients are marked. If
   any recipient failed temporarily, the signal is returned with a delay by
   its delivery number (5 s, 30 s, 2 min, 10 min) and the redelivery sends
   only to recipients without a mark; after the fifth failed delivery it is
   terminated and counted.
7. Only FCM's verdict `UNREGISTERED` deletes the token that was sent. A bare
   `404` (a proxy or a wrong endpoint) and `SENDER_ID_MISMATCH` (credentials
   of another Firebase project) are configuration errors and keep the token.
   Network errors, access-token failures, `401`, `429` and `5xx` are
   temporary; other answers are final for that device.

### Guarantee

At least once from the `PubAck` to FCM accepting the message
([ADR 0003](adr/0003-push-signals-jetstream.md)). Outside it, delivery stays
best effort: Chat failing between the commit and the `PubAck`, the broker or
stream being unavailable or full (the signal is counted as lost), a signal
older than an hour (acknowledged unsent), and anything after FCM accepted
it. A repeated push is possible (a crash between FCM accepting it and the
mark being written repeats it once for that recipient) and harmless, since
a client's sync is idempotent. Collapse keys do not remove duplicates.

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
- **Broker.** NATS JetStream rather than Redis Pub/Sub: signals published
  while Auth restarts are kept, and acknowledgement, redelivery delays,
  deduplication and per-subject permissions come from the broker. Live chat
  events stay on Redis; the client's catch-up already recovers them.
- **Auth declares the stream.** Auth creates or updates the stream and its
  consumer at start and again when the broker lost them (an empty store).
  There is no separate administrative identity; Auth's permissions name
  this stream and consumer only and exclude deleting or purging.

### Trust boundaries

- Chat, Auth → broker: TLS 1.3 with the services' internal certificates
  ([mTLS](mtls.md)); the broker maps the certificate URI to the user, so
  there are no passwords. Chat may only publish to `push.signal.v1` and read
  its own inbox; Auth may only declare, read and acknowledge
  `PUSH_SIGNALS`/`auth-push`. Any other certificate of the deployment CA,
  a foreign CA or no certificate gets no session. Both services verify the
  broker's exact identity `spiffe://vault-chat/service/nats`, and the broker
  requires mTLS mode; there is no plaintext fallback.
- Broker → Auth: anyone who can publish to the subject (only Chat's
  certificate) can make Auth wake devices of arbitrary users. Signals carry
  no content, are validated and collapsed, so the effect is bounded to one
  wake-up per user and room per window.
- The broker is on the internal network `push-signals`, shared with Chat and
  Auth only, with no host port. Its configuration is
  [`deployments/nats/nats.conf`](../deployments/nats/nats.conf).
- Auth → FCM: needs egress to `oauth2.googleapis.com` and
  `fcm.googleapis.com`; Google learns device tokens and room ids, never text.
- Provider answers decide deletion of exactly the token that was sent.
- Revocation → provider request: the authorization of each request is the
  check made right before it starts (`IsPushTarget`, the access-token rule on
  the current rows). A logout, revoke-all, refresh reuse, credential change
  or a move of the token to another login that committed before that check
  stops the request, even when the user's tokens were loaded earlier and
  other devices of the user were being sent to meanwhile. A revocation that
  commits after the check does not stop that request: it is already in
  flight, like a push FCM has accepted, which cannot be recalled. The window
  is the time between one indexed query and the start of the HTTP request;
  the payload is a room id only, and the next signal for that device is
  refused. A redelivered signal is checked again in the same way.

### Resource budgets

- Stream: on disk, at most 100 000 signals, 256 MiB and one hour per signal;
  when full it refuses new signals instead of evicting unread ones. The
  broker's file store is limited to 512 MiB, memory store to 16 MiB,
  connections to 32, messages to 32 KiB; the container to 128 MiB.
- Chat: the PubAck wait shares the event worker and its 1-second timeout;
  publishing while disconnected fails at once instead of buffering, and
  futures without an answer are released after 5 seconds.
- Auth: at most 20 signals unacknowledged (`MaxAckPending`), so at most
  10 000 user/room pairs, the dispatcher's bound. Four concurrent sends;
  each user's token load, sends and mark share a 10-second deadline; at
  most 10 tokens per user. HTTP answers are read up to 64 KiB.
- Marks: at most 500 rows per signal, kept two hours, deleted every minute in
  batches of 10 000.
- Shutdown stops taking work and returns the signals in work to the stream
  for immediate redelivery; sends in progress finish within their own
  10-second deadline, and Auth waits at most 15 seconds.

### Failure behavior

| Failure | Behavior |
|---|---|
| Auth stopped or restarting | Signals wait in the stream (up to an hour and the limits) and are delivered after start |
| Broker unavailable | Sending messages is unaffected; Chat counts the signals as lost; both services reconnect every second |
| Broker restarted with an empty store | Auth declares the stream and consumer again; signals published before are lost |
| Stream full | Publication is refused and counted; Chat is not blocked |
| Auth database or FCM temporarily unavailable | Delayed redelivery, at most 5 deliveries, then `TERM`, a counter and a log line |
| Auth crashed during a send | Redelivered 30 seconds after the last `InProgress`; marked recipients are skipped |
| Mark cannot be written | Treated as a temporary failure: the push may repeat once |
| Malformed signal | `TERM` at once, counted, logged at most every 10 seconds |

Metrics of Auth: `vault_push_pending`, `vault_push_sent_total`,
`vault_push_failures_total`, `vault_push_dropped_total` (recipients that
did not fit and are retried), `vault_push_invalid_tokens_total`,
`vault_push_malformed_signals_total`, `vault_push_skipped_total`,
`vault_push_expired_total`, `vault_push_signals_in_work`,
`vault_push_signal_deliveries_total`, `vault_push_signals_acked_total`,
`vault_push_signals_retried_total`, `vault_push_signals_terminated_total`,
`vault_push_signals_exhausted_total`, and the stream's
`vault_push_stream_pending` and `vault_push_stream_ack_pending` (refreshed
every 15 seconds, `-1` when unknown). Chat: `vault_push_signals_stored_total`
and `vault_push_signals_lost_total`. Alerts: `PushSignalsLost`,
`PushSignalsBacklog` (more than 1000 waiting for 10 minutes),
`PushStreamUnreachable` and `PushSignalsExhausted`.

### Acceptance tests

The broker tests run the deployment's `nats.conf` in process with synthetic
certificates; numbers in parentheses refer to ADR 0003.

1. A new message signals its recipients except the sender, without text or
   message id; membership events and messages only the sender receives
   signal nothing; large rooms are split with an id per part.
2. Signals are validated and fuzzed; malformed, oversized, non-canonical and
   over-long recipient lists are rejected.
3. Collapsing: the first signal is sent at once, 50 in the window become one
   push after it and finish only then, other rooms are separate, quiet pairs
   are forgotten; every merged signal is marked after the push.
4. Only rejected tokens are deleted; temporary failures are retried, final
   ones are not; a failed check stops the pair; the pending bound retries
   recipients that do not fit.
5. FCM: a data-only body with exactly the documented fields and the TTL of
   the signal's remaining hour, a valid RS256 assertion, cached access tokens
   renewed before expiry, error answers mapped without leaking the token and
   classified as temporary or final.
6. Broker: Chat and Auth have only their permissions; other identities of
   the CA, a foreign CA and a client without certificate are refused (12);
   a repeated id is stored once (8); with the broker down, events are
   delivered and signals counted lost, and storing resumes after it returns
   (11); a full stream refuses new signals and keeps the unread one (13).
7. Consumer: signals stored while Auth was down are delivered (1); marked
   recipients are skipped (2); a lost mark repeats one push to that
   recipient only (3); partial success redelivers to the failed recipient
   only (4); a signal waiting longer than `AckWait` is kept in progress (5);
   the 21st signal waits until one of 20 is acknowledged (6); a signal 50
   minutes old gets a TTL of about 10 minutes and one over an hour is
   acknowledged unsent (7); malformed signals are terminated without
   redelivery (9); temporary failures are retried after growing delays and
   terminated after the fifth delivery, final answers acknowledged (10);
   signals in work are returned at stop; a lost stream is declared again.
8. End to end with PostgreSQL: Chat's publisher, the broker, Auth's consumer
   and dispatcher; Auth starts after the message was sent; the sender and a
   revoked login get nothing, a rejected token is deleted, an active device
   is woken, every recipient is marked.
9. With PostgreSQL and one worker: a logout, a revoke-all or a move of the
   token to another user, committed while an earlier device of the user is
   being sent to, stops the send to the affected device.
10. Marks: order, repeated and concurrent writes, expiry and batched
    deletion against PostgreSQL; the database refuses ids of another form.
11. Container: `tests/compose/push_broker_acceptance.py` starts the broker of
    the push overlay with certificates from `scripts/gen-rpc-certs.py`,
    checks its user, read-only root, capabilities, memory, mounts and
    internal network, stores a signal as Chat, refuses Gateway, Media and
    Preview, restarts the container and receives the signal as Auth.

### Operator configuration

Push stays disabled until an operator enables it. It needs mTLS, which the
VPS requires by default. On the VPS, from the checkout (`/opt/vault-chat`):

1. Create `secrets/push` with owner `65532:65532` and mode `0700`.
2. Copy the Firebase service account file to `secrets/push/fcm.json` through a
   private channel, with owner `65532:65532` and mode `0400`. Auth runs as UID
   65532 and refuses a file that others can read. It must belong to the same
   Firebase project as the Android build.
3. Run `scripts/vps.sh mtls init` to issue the broker's certificate
   `secrets/rpc/nats`; the CA and the existing service certificates are kept.
4. Copy `deployments/push.env.example` to `.env.push`, mode `0600`.
5. Allow outbound HTTPS to `oauth2.googleapis.com` and `fcm.googleapis.com` in
   the host or provider policy.
6. Run `scripts/vps.sh up`.

The presence of `.env.push` makes the VPS helper add
`deployments/docker-compose.push.yml`. The overlay mounts the key into Auth
read-only with automatic host-directory creation disabled, adds a dedicated
bridge network for Auth's outbound traffic, and starts the broker on the
internal network `push-signals` with its certificate and configuration
mounted read-only and its store on the volume `natsdata`. No host ports are
published. `scripts/vps.sh mtls rotate` restarts the broker with its new
certificate together with the services. Backups include the opt-in and the
encrypted key but not the stream: signals expire within an hour, and Auth
declares an empty stream again after a restore. To pause push, leave the
variable in `.env.push` empty: then Chat publishes no signals and Auth
consumes none. To stop for good, also delete the key from Firebase.

For local development, push needs both overlays:
`docker compose -f deployments/docker-compose.yml -f deployments/docker-compose.mtls.yml -f deployments/docker-compose.push.yml --env-file .env.push up -d`
after `python3 scripts/gen-rpc-certs.py secrets`.

These steps were checked against rendered Compose configuration, synthetic
fixtures and the disposable broker acceptance, not against a real Firebase
project or the VPS.
