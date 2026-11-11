# Calls

The call control plane of [ADR 0005](../docs/adr/0005-call-control.md) in
Erlang/OTP. This is stage E2: a prototype to measure, not a deployed service.

## Toolchain

Erlang/OTP 28.5.0.8 and rebar3 3.27.3, pinned by the ADR. PostgreSQL access uses
the locked `epgsql` 4.8.0 dependency.

```sh
xmake calls          # EUnit, xref and Dialyzer
cd calls && rebar3 eunit
```

The storage tests require a disposable loopback PostgreSQL. They never use an
inherited development or production database:

```sh
docker run -d --name vault-calls-dev-postgres \
  --tmpfs /var/lib/postgresql/data \
  -e POSTGRES_USER=calls -e POSTGRES_PASSWORD=calls -e POSTGRES_DB=calls_test \
  -p 127.0.0.1::5432 postgres:16-alpine
PORT=$(docker port vault-calls-dev-postgres 5432/tcp | sed 's/.*://')
export VAULT_CHAT_TEST_CALLS_DATABASE_URL="postgres://calls:calls@127.0.0.1:${PORT}/calls_test?sslmode=disable"
xmake calls
docker stop vault-calls-dev-postgres
docker rm vault-calls-dev-postgres
```

`xmake ci` creates this fixture automatically, runs `xmake calls` and removes the
owned Compose project. `xmake doctor` refuses other versions of OTP or rebar3.

## What exists

- `vault_call`: one `gen_statem` process per call. It orders the call's
  commands, checks the caller's and the callee's device, applies the signal
  matrix and owns the timers: ringing 30 s, connecting 30 s, a heartbeat from
  each device within 45 s, at most 2 h active. Accepted commands commit before
  the in-memory state changes; timers restart from their persisted deadlines.
  A repeated command id returns its original response, including after `ended`;
  a different request fingerprint receives `command_id_reused`.
- `vault_call_sup`: the calls' supervisor; an abnormal exit restores only that
  call from its row. Application startup recovers every unfinished call.
- `vault_calls_registry`: names calls by id, monitored, in a protected ETS table.
- `vault_calls_store`: transactional creation and conditional transitions with
  one active call per user, capacity 200, and a command journal with SHA-256
  fingerprint replay checks. Ending releases participants and capacity in the
  same transaction. The signal journal assigns per-call `signal_seq`, replays
  matching `sender_seq` and supports catch-up with `signals_after`. Signals are
  limited to 20/s per device, 200 per call and 32 candidates per device; SDP is
  capped at 8 KiB and candidates at 512 bytes. Commands expire after 24 h and signals one hour
  after `ended`, with cleanup at startup and hourly. Database statements and
  locks have bounded timeouts.

Not yet: the HTTP prototype interface, Auth and Chat adapters and measurements
(E2.3).
