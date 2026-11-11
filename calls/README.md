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
  A repeated command id returns its original response, including after `ended`,
  without changing process state or timers; a different request fingerprint
  receives `command_id_reused`.
- `vault_call_sup`: the calls' supervisor; an abnormal exit restores only that
  call from its row. Application startup recovers every unfinished call.
- `vault_calls_registry`: names calls by id, monitored, in a protected ETS table.
- `vault_calls_store`: transactional creation and conditional transitions with
  one active call per user, capacity 200, and a command journal with SHA-256
  fingerprint replay checks. Ending releases participants and capacity in the
  same transaction. The signal journal assigns per-call `signal_seq`, replays
  matching `sender_seq` and supports catch-up with `signals_after`. Signals are
  limited to 20/s per device, 200 per call and 32 candidates per device; SDP is
  capped at 8 KiB and candidates at 512 bytes. Commands expire after 24 h and
  signals one hour after `ended`, with cleanup at startup and hourly. Database
  statements and locks have bounded timeouts.
- `vault_calls_http`: a loopback-only HTTP/1.1 prototype with 32 headers,
  16-KiB bodies and at most 200 connections. `vault_calls_auth_stub` and
  `vault_calls_chat_stub` provide synthetic sessions and direct rooms with
  configurable delays and outage switches; they are not production adapters.

## Load acceptance

`VAULT_CALLS_LOADGEN_COUNT` defaults to 200. With the disposable database URL
above, `scripts/dev/calls-load` starts the HTTP prototype and runs full call
lifecycles in batches of eight concurrent clients. It measures command p95/p99,
throughput, scheduler utilization and RSS sampled every 20 ms. One process is
killed and recovered, then the whole VM is killed with `kill -9` and a second VM
verifies all 200 calls. Latency or RSS-growth failure is reported only after the
restart check. The metrics JSON includes `baseline_rss_bytes`, `rss_delta_bytes`
and `gate_status`; a high baseline is reported as `baseline_rss_exceeded` rather
than a passed absolute RSS gate. `xmake ci` runs this scenario after the Calls
checks.

The 2026-11-11 local run completed 200 calls and 1600 commands with zero errors,
177.9 commands/s, p95 81.46 ms and p99 121.64 ms; crash and restart recovery both
passed. The host BEAM baseline was 2.06 GiB and the sampled peak delta was about
24.5 MiB, so the 256-MiB absolute RSS gate remains explicitly unmet on this host
(`baseline_rss_exceeded`). This is prototype evidence, not production acceptance.

Not yet: production adapters.
