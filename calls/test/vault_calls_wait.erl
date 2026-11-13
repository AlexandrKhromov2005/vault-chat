-module(vault_calls_wait).
-moduledoc """
Bounded waits for the asynchronous parts of call recovery in EUnit tests.

A supervisor restarts a killed call, and a call recovered at startup expires,
after the test has moved on. A wait observes the state it needs instead of
assuming that a fixed delay was long enough. The deadline stays bounded, and a
wait that runs out fails with the last observed state.
""".

-export([until/2, restarted/2, ended/1]).

-define(DEADLINE, 5000).
-define(INTERVAL, 5).

-doc "Calls `Probe` until `Done` accepts its value, and returns that value.".
-spec until(fun(() -> Value), fun((Value) -> boolean())) -> Value.
until(Probe, Done) ->
    poll(Probe, Done, erlang:monotonic_time(millisecond) + ?DEADLINE).

-spec poll(fun(() -> Value), fun((Value) -> boolean()), integer()) -> Value.
poll(Probe, Done, Deadline) ->
    Value = Probe(),
    case Done(Value) of
        true -> Value;
        false ->
            case erlang:monotonic_time(millisecond) >= Deadline of
                true -> error({wait_timeout, Value});
                false ->
                    timer:sleep(?INTERVAL),
                    poll(Probe, Done, Deadline)
            end
    end.

-doc "Waits until the supervisor has registered a live process of call `Id` other than `Old`.".
-spec restarted(binary(), pid()) -> pid().
restarted(Id, Old) ->
    until(fun() -> vault_calls_registry:whereis_name(Id) end,
          fun(Pid) -> is_pid(Pid) andalso Pid =/= Old andalso is_process_alive(Pid) end).

-doc "Waits until the process of call `Id`, if there is one, has exited.".
-spec ended(binary()) -> ok.
ended(Id) ->
    case vault_calls_registry:whereis_name(Id) of
        undefined -> ok;
        Pid ->
            Ref = monitor(process, Pid),
            receive {'DOWN', Ref, process, Pid, _Reason} -> ok
            after ?DEADLINE ->
                error({wait_timeout, Id, erlang:process_info(Pid, [current_stacktrace, message_queue_len])})
            end
    end.
