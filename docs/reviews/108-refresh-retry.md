# Refresh retry review

## Invariant and acceptance

The client presents a signed refresh token through Gateway and Auth. PostgreSQL
serializes rotations under the existing per-user advisory lock. A retry may
return only its original, active successor within the configured window. Both
deadlines must be checked after acquiring that lock. The window starts when the
rotation occurs, rather than when its transaction began waiting.

The correction adds no requests, session rows, signatures or production workers.
Existing gateway limits, transaction cancellation and database error handling
remain applicable. An expired retry returns `ErrSessionReused`, preserving the
service's session-revocation response. Database failures remain internal errors.

The committed scenarios use `newSessionFixture` and the disposable PostgreSQL
fixture from `deployments/docker-compose.ci.yml`. Each scenario has a ten-second
context, observes an actual advisory-lock waiter and holds the lock past a
two-second deadline. No external service or real credentials are used.

## Behavioral reproduction

All three scenarios failed on PR head `3ed2357c7eb6e6a2959d3954b78b1c2325cdebce`:

| Scenario | Observed failure before correction | Required result |
| --- | --- | --- |
| Retry waits beyond its window | Rotation returned nil error | Reject replay |
| Successor expires while retry waits | Rotation returned nil error | Reject replay |
| Initial rotation waits longer than its retry window | Immediate retry returned `ErrSessionReused` | Return the original successor |

PostgreSQL `now()` retains the transaction-start time across lock waits.
`clock_timestamp()` records the rotation and evaluates retry deadlines using the
current database time. All three scenarios pass after this correction.

With `VAULT_CHAT_TEST_DATABASE_URL` set to the disposable auth database, run:

```sh
go test -race ./tests/integration -run '^(TestSessionRepository|TestRefreshToken|TestGateway_SessionLifecycle)' -count=1
```

This also exercises the existing concurrent refreshes, disabled retry window,
wrong user, logout, expired successor, stale replay and HTTP session lifecycle.
Full commit-bound evidence still requires `xmake ci` and publication from the
exact final commit, followed by another participant's review.

## Rejected findings

- Reissuing a JWT would extend the persisted login: rejected. The retry does not
  update `sessions.expires_at`; access validation checks the active family in the
  database, and rotation checks the stored expiry. The response expiry is capped
  separately. The JWT's own expiry is not the only authorization check.
- Simultaneous retries would create separate successor chains: rejected. The
  per-user lock and existing concurrent repository/service scenarios establish
  one successor, with no additional session rows for retries.
- The failed initial web test run indicated a regression: rejected. The shell
  selected Node 20.18.1. Using the documented `.tools/bin` PATH selected pinned
  Node 22.23.4; `xmake test` and `xmake lint` then passed.

No schema migration, deployment or production configuration is changed.

## Integration with email account security

Rotation and retry both retain the current user's credential-version and email
ownership checks under the shared user lock. A rotated row from an older
credential epoch returns `ErrSessionRevoked` before reuse classification, so it
cannot revoke newer logins. Successors must belong to the same user and current
epoch, and the returned session retains that version.

The existing stale-password, rotated-old-epoch and verification-policy scenarios
run alongside retry, lock-wait and HTTP lifecycle regressions against disposable
PostgreSQL with the race detector. The two existing repository callers use the
strict zero-window adapter; their original security assertions are preserved.
