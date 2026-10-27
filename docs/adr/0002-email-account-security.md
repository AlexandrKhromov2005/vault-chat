# Email account security (phase one)

## Scope and rollout

Verify email ownership, recover a password, change a verified email with fresh
credentials, and notify affected mailboxes of completed security changes.
Invitation-only registration remains. MFA, social sign-in and device management
are separate future work. SMTP delivery is explicitly enabled after configuration;
before adoption, disabled mail keeps the invitation flow and exposes no working recovery
endpoint. Production delivery is not assumed from a successful local test.

Delivery maintenance must not create legacy accounts after enrollment: once a
saved required account exists, the legacy invitation path refuses registration
until delivery is enabled again. Verification moves legacy accounts into this
saved enrollment state. Both registration paths serialize through the outbox lock.

New public accounts created with mail enabled require verification before any
access/refresh session is issued. Existing accounts are not silently marked
verified. An optional operator-configured legacy verification deadline permits a
controlled transition, including existing sessions and refresh. Password recovery
only targets a previously verified address. Verification resend requires the
account's password, so possession of a mistakenly recorded legacy address alone
cannot reclaim an existing account and its chat history.

## Invariants and trust boundaries

- Tokens have 256 random bits, are hashed in the action table and bound to purpose,
  user, current email and credential version. Verification lasts 24h, reset 15m,
  email change 30m. A token is consumed once in the same transaction as its effect.
- Anonymous reset requests do not invalidate earlier valid reset links. A
  completed reset advances the version and invalidates them all. Authenticated
  verification/change resends may supersede the previous proof.
- Links use the configured trusted origin and a URL fragment. GET/preview never
  consumes a token; an explicit POST confirms the action. No arbitrary redirect,
  request Host-derived link, URL token logs, or automatic login after recovery.
- Public requests use generic responses for unknown, unverified, wrong-password
  and account-quota cases. IP credential quotas apply before database/password
  work; account mail requests are limited to one/minute and five/hour/purpose.
- Registration consumes its invitation, creates the pending account/action and
  enqueues the encrypted message atomically. Delivery failures never bypass email
  verification, and normal verified login/chat does not depend on SMTP.
- Reset/change increments the credential version, invalidates old actions,
  revokes all access/refresh session families and enqueues notices atomically.
  Fresh password verification is required to request an email change. Its final
  unique-email check happens in the committing transaction.
- Session creation/rotation and account credential mutations take the existing
  per-user advisory lock. Sessions record the credential version; stale password
  work cannot create a surviving session after a concurrent reset/change.
- Only message ciphertext is retained in the outbox (AES-256-GCM, random nonce,
  separate 32-byte key file). Token hashes alone are not a claim that queued mail
  is token-free: its encrypted payload includes the link until delivery/expiry.

## Delivery and resource budgets

One worker sends one message at a time, with a 10s delivery deadline, at most five
attempts and bounded retry delays. PostgreSQL claims use leases and SKIP LOCKED.
The global queue is capped at 1000 pending messages; successful, expired and
permanently failed payloads are deleted. Payloads are bounded to 64 KiB. SMTP
requires validated TLS (STARTTLS or implicit TLS); no remote plaintext fallback.
Logs expose neither addresses, secrets, message bodies nor raw SMTP replies.
The existing two Argon2 slots apply to verification resend and email change.
Raw SMTP credentials and outbox encryption keys are loaded from private files.

## Acceptance evidence

Before enabling delivery, regressions must cover:

1. Required/unverified login, refresh, API and WebSocket access fail; legacy
   accounts retain the documented grace policy without becoming verified.
2. Unknown/unverified reset requests have the same external result; wrong
   password verification requests never send a usable confirmation.
3. Expired, wrong-purpose, reused and superseded tokens cannot change state.
4. Simultaneous consumers cause one effect; password reset racing with login or
   rotation cannot leave a session created from the old credential alive.
5. Duplicate/case-variant target emails cannot be reassigned or leak availability.
6. Failed transactions leave the token, account, sessions and outbox consistent;
   unavailable/full mail storage cannot partially commit a security change.
7. SMTP trust/STARTTLS failures, timeouts, malformed headers, retries, corrupt
   ciphertext, worker restart and expiry do not leak data or create hot loops.
8. UI does not persist link secrets, act on GET, auto-login after reset, or let
   delayed responses alter a different user's session.

Use synthetic databases, local TLS SMTP fixtures and private temporary keys.
Do not send real mail as a test. Relevant guidance:
[OWASP password recovery](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html)
and [email identity workflows](https://cheatsheetseries.owasp.org/cheatsheets/Email_Validation_and_Verification_Cheat_Sheet.html).
