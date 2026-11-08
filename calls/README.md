# Calls

The call control plane of [ADR 0005](../docs/adr/0005-call-control.md) in
Erlang/OTP. This is stage E2: a prototype to measure, not a deployed service.

## Toolchain

Erlang/OTP 28.5.0.8 and rebar3 3.27.3, pinned by the ADR. This stage uses only
OTP; there are no dependencies to fetch.

```sh
xmake calls          # EUnit, xref and Dialyzer
cd calls && rebar3 eunit
```

`xmake calls` is not part of `xmake ci` yet; CI adds the pinned Erlang tools in
a separate change.

## What exists

- `vault_call`: one `gen_statem` process per call. It orders the call's
  commands, checks the caller's and the callee's device, applies the signal
  matrix and owns the timers: ringing 30 s, connecting 30 s, a heartbeat from
  each device within 45 s, at most 2 h active.
- `vault_call_sup`: the calls' supervisor; a crash ends only that call.
- `vault_calls_registry`: names calls by id, monitored, in a protected ETS table.

Not yet: PostgreSQL state, command and signal logs, admission and restart from
the database (E2.2); the HTTP prototype interface, Auth and Chat adapters and
measurements (E2.3). Until then a call lives only in its process.
