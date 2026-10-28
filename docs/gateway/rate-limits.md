# Gateway rate limits

Gateway admits requests through token buckets: a bucket holds up to *burst*
tokens and refills at *per minute* tokens per minute; each request takes one.
Clients are identified by their TCP peer address (`RemoteAddr`); IPv4-mapped
IPv6 uses the IPv4 key and other IPv6 addresses share their /64 network. The
gateway trusts no forwarding header, so it must receive client connections
directly.

## Problem

Until this change every credential request (login, registration, email and
recovery flows) took a token from one bucket of 5/minute per IP, and every API
request took one from 120/minute per IP before authentication. Many users of a
mobile carrier NAT or of a university Wi-Fi share one address, so the sixth
person to log in within a minute got `429`, and a few active users exhausted the
API quota of everyone behind the same address. A refresh that fails with `429`
on a phone looks like a lost session.

Neither bucket distinguished an attacker from a crowd: successful requests,
which prove possession of a password, token or invitation, cost the same as
guesses.

## Decision

Keys combine the address with what the request proves, and a request that
proves possession of a credential gives back the per-address token it took.

| Bucket (key) | Default | Routes | Token returned when | Redis outage |
|---|---|---|---|---|
| API per address (`ip:<addr>`) | 120/min, burst 30 | every `/api/` route | the access token is valid, or a login, registration or refresh succeeds | fails open |
| API per user (`user:<id>`) | 120/min, burst 30 | authenticated routes | never | fails open |
| Credentials per address (`auth-ip:<addr>`) | 10/min, burst 10 | login, registration, email and recovery flows | login `200`, registration `201` | `503` |
| Credentials per address and subject (`auth:<addr>:<subject>`) | 5/min, burst 5 | same | never | `503` |
| Refresh per address (`refresh-ip:<addr>`) | 30/min, burst 30 | refresh | refresh `200` | local bucket only |

The *subject* is a SHA-256 of the lower-cased `email` (login, registration,
email and recovery requests), `new_email` (email change request) or `token`
(confirmations). Bodies that are not JSON or carry none of these fields share
the subject `-`, so malformed traffic keeps a single strict bucket per
address. Credential and refresh buckets are enforced both in process memory
and in Redis, as before. Settings: `GATEWAY_AUTH_RATE_LIMIT_*` keep their
defaults but now apply per address and subject; `GATEWAY_AUTH_IP_RATE_LIMIT_*`
and `GATEWAY_REFRESH_RATE_LIMIT_*` are new.

Why this keeps brute force bounded:

- Guessing one account's password: at most 5 attempts per minute per address
  at Gateway, shared by all Gateway instances through Redis, and at most 5 per
  minute per account across all addresses in Auth (unchanged). Successful
  logins are not refunded in these buckets.
- Spraying passwords over many accounts from one address: every failed
  attempt keeps its token in `auth-ip`, so an address gets 10 failures per
  minute (before: 5 attempts, successful or not). A spray succeeds rarely, so
  refunds do not help it. An attacker can only get back the token of the
  request that succeeded, never a token of another request, so interleaving a
  known good login does not buy guesses.
- Registration needs a single-use invitation bound to the email, so each
  refunded registration consumes an invitation issued by the operator.
- Refresh tokens and email proofs are 256-bit secrets; their buckets bound the
  cost of garbage traffic, not guessing.
- Authenticated API traffic is bounded per user. Requests with missing, invalid
  or expired tokens keep their address token, so token validation calls to
  Auth stay bounded per address.

The relaxation is deliberate: an address with many legitimate users now gets
10 failed credential attempts per minute in total, twice the old attempt
budget, and an unlimited number of successful ones, still within the per-user
and per-subject buckets.

## Trust boundaries

- Client → Gateway: untrusted body, headers and address. The subject is taken
  from the body before Auth validates it; a forged subject only selects which
  strict bucket the request is charged to, and the per-address buckets apply
  regardless of the body.
- Gateway → Redis: buckets are shared state; a Redis that refuses or loses
  writes cannot lift the process-local buckets.
- Gateway → Auth: Auth remains the authority on credentials and keeps its own
  per-account limit and two concurrent password operations per process.

## Resource budgets

- The subject is read from the body already bounded by `GATEWAY_MAX_BODY_BYTES`
  and the request deadline. The body is buffered once and replayed to the
  handler, so a credential request holds at most two copies of a 1 MiB body.
- Local tables: 4096 addresses per credential and refresh bucket, 16384
  address/subject pairs. A full table refuses new keys (`429`) rather than
  evicting quotas; only full buckets are removed.
- A refund is one more Redis call per successful request, bounded by the Redis
  client timeouts (250 ms) and run after the response status is known.

## Failure behavior

- Redis unavailable: credential routes answer `503` (unchanged); refresh uses
  only its local bucket; other API routes fail open (unchanged).
- A failed refund is logged and ignored: the client keeps the stricter quota.
- A token is returned at most once and only if the request actually took it;
  a request let through because the limiter failed returns nothing.
- Concurrent requests take their tokens before any of them is served, so a
  burst of simultaneous requests cannot exceed the bucket even if some of them
  later succeed.

## Acceptance tests

Unit and router tests, `internal/gateway/...`:

1. Twenty users behind one address log in successfully within a minute; all get
   `200` (failed before: `429` from the sixth).
2. Six wrong passwords for one account from one address: the sixth gets `429`;
   a different account from the same address is still admitted.
3. Eleven failed logins for different accounts from one address: the eleventh
   gets `429`; successful logins in between do not restore a failed token.
4. Twenty concurrent failed logins from one address: at most ten reach Auth.
5. Malformed or missing bodies share the strict `-` subject.
6. Refresh: many successful refreshes from one address are admitted; the
   thirty-first failure in a minute gets `429`; refresh keeps working on its
   local bucket when Redis fails.
7. Many users with valid access tokens behind one address exceed the address
   quota without `429`; invalid tokens from that address are still limited.
8. Refund semantics on the memory and Redis limiters: capped at burst, no
   effect on a missing bucket, nothing returned after a failed `Allow`.
9. Credential routes still answer `503` without Redis.

## Not covered

- An attacker controlling many addresses (botnet, many IPv6 /64 networks) is
  limited only by the per-account bucket in Auth and by its two concurrent
  password operations; per-address limits cannot stop that.
- One abuser behind a shared address can still spend its failure budget, and
  then successful logins from that address wait for a refill as well: tokens
  are taken before the outcome is known. Legitimate traffic alone no longer
  exhausts it.
- WebSocket upgrades authenticate in-band and keep their per-address token:
  one upgrade per reconnect fits the address quota. File transfers are
  authenticated routes and are refunded like the rest of the API.
- Auth's per-account bucket is per Auth process (unchanged).
