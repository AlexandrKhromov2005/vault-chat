-module(vault_call).
-moduledoc """
One call, one process (docs/adr/0005-call-control.md). It orders the
commands of the call and owns its timers:

```
ringing    --accept-->                       connecting
connecting --connected from both devices-->  active
ringing    --reject | cancel | timeout-->    ended
connecting --end | timeout-->                ended
active     --end | heartbeat missed | max--> ended
```

The caller's device is the one that started the call; the callee's device
is any of the callee's devices until one accepts, and then only that one.
Strangers get `not_found`; participants in the wrong role or state get
`conflict`. A database commit precedes every in-memory change. After an
abnormal exit the supervisor restores the process from the database row.
""".
-behaviour(gen_statem).

-export([start_link/1, command/2, state/1]).
-export([callback_mode/0, init/1, handle_event/4]).
-export_type([start_args/0, command/0, reply/0]).

-type user() :: binary().
-type device() :: binary().
-type state_name() :: ringing | connecting | active.
-type role() :: caller | callee.
-type signal_kind() :: offer | answer | candidate | end_of_candidates.
-type start_args() :: #{id := binary(), room_id := binary(), caller := user(), caller_device := device(),
                        callee := user(), command_id := binary(), fingerprint := binary()}.
-type restore_args() :: #{id := binary()}.
-type command() :: {accept | reject | cancel | connected | 'end' | heartbeat, user(), device()}
                 | {signal, user(), device(), signal_kind()}.
-type reply() :: {ok, state_name() | ended} | {error, not_found | conflict | command_id_reused | badarg | unavailable}.
-type data() :: #{id := binary(), room_id := binary(), state := state_name() | ended,
                  state_version := non_neg_integer(), caller := user(), caller_device := device(), callee := user(),
                  callee_device := device() | undefined, connected := [role()], offered := boolean(),
                  pending_offer := none | role(), state_entered_at := integer(),
                  caller_heartbeat_at := integer(), callee_heartbeat_at := integer()}.

-spec start_link(start_args() | restore_args()) -> gen_statem:start_ret().
start_link(Args) ->
    start_registered(Args, true).

-spec start_registered(start_args() | restore_args(), boolean()) -> gen_statem:start_ret().
start_registered(#{id := Id} = Args, Retry) ->
    case gen_statem:start_link({via, vault_calls_registry, Id}, ?MODULE, Args, []) of
        {error, {already_started, _Pid}} when Retry ->
            timer:sleep(5),
            start_registered(Args, false);
        Result -> Result
    end.

-doc "Runs a command; an unknown or ended call is `not_found`.".
-spec command(binary(), command()) -> reply().
command(Id, Command) ->
    try gen_statem:call({via, vault_calls_registry, Id}, {Command, meta(Id, Command, command_device(Command))}, 5000)
    catch
        exit:{noproc, _} -> {error, not_found};
        exit:{normal, _} -> {error, not_found}
    end.

-spec state(binary()) -> {ok, state_name()} | {error, not_found}.
state(Id) ->
    try gen_statem:call({via, vault_calls_registry, Id}, get_state, 5000)
    catch
        exit:{noproc, _} -> {error, not_found};
        exit:{normal, _} -> {error, not_found}
    end.

-spec callback_mode() -> gen_statem:callback_mode_result().
callback_mode() ->
    [handle_event_function, state_enter].

-spec init(start_args() | restore_args()) -> gen_statem:init_result(state_name()).
init(#{id := Id}) ->
    case vault_calls_store:get_call(vault_calls_store, Id) of
        {ok, #{state := State} = Data} -> {ok, State, Data};
        {error, Reason} -> {stop, Reason}
    end.

-spec handle_event(gen_statem:event_type(), term(), state_name(), data()) ->
    gen_statem:event_handler_result(state_name()).
handle_event(enter, _Old, ringing, Data) ->
    {keep_state_and_data, [{state_timeout, remaining(Data, timeout(ringing_timeout)), expired}]};
handle_event(enter, _Old, connecting, Data) ->
    {keep_state_and_data, [{state_timeout, remaining(Data, timeout(connecting_timeout)), expired}]};
handle_event(enter, _Old, active, Data) ->
    {keep_state_and_data, [{state_timeout, remaining(Data, timeout(max_call_duration)), expired},
                           {{timeout, {heartbeat, caller}}, heartbeat_remaining(caller, Data), missed},
                           {{timeout, {heartbeat, callee}}, heartbeat_remaining(callee, Data), missed}]};
handle_event(state_timeout, expired, _State, Data) ->
    expire(Data);
handle_event({timeout, {heartbeat, _Role}}, missed, active, Data) ->
    expire(Data);
handle_event({call, From}, get_state, State, _Data) ->
    {keep_state_and_data, [{reply, From, {ok, State}}]};
handle_event({call, From}, {Command, Meta}, State, Data) when tuple_size(Command) >= 3 ->
    case role(element(2, Command), element(3, Command), Data) of
        stranger -> {keep_state_and_data, [{reply, From, {error, not_found}}]};
        Role -> run(Command, Role, State, Data, From, Meta)
    end;
handle_event({call, From}, _Malformed, _State, _Data) ->
    {keep_state_and_data, [{reply, From, {error, badarg}}]};
handle_event(info, _Message, _State, _Data) ->
    keep_state_and_data.

-spec role(user(), device(), data()) -> role() | other_device | stranger.
role(Caller, Device, #{caller := Caller, caller_device := Device}) -> caller;
role(Caller, _Device, #{caller := Caller}) -> other_device;
role(Callee, _Device, #{callee := Callee, callee_device := undefined}) -> callee;
role(Callee, Device, #{callee := Callee, callee_device := Device}) -> callee;
role(Callee, _Device, #{callee := Callee}) -> other_device;
role(_User, _Device, _Data) -> stranger.

-spec run(command(), role() | other_device, state_name(), data(), gen_statem:from(), map()) ->
    gen_statem:event_handler_result(state_name()).
run({accept, _, Device}, callee, ringing, Data, From, Meta) ->
    commit(connecting, Data#{callee_device := Device}, From, Meta, ringing);
run({reject, _, _}, callee, ringing, Data, From, Meta) ->
    commit(ended, Data, From, Meta, ringing);
run({cancel, _, _}, caller, ringing, Data, From, Meta) ->
    commit(ended, Data, From, Meta, ringing);
run({'end', _, _}, Role, State, Data, From, Meta) when Role =:= caller; Role =:= callee ->
    commit(ended, Data, From, Meta, State);
run({connected, _, _}, Role, connecting, #{connected := Connected} = Data, From, Meta)
  when Role =:= caller; Role =:= callee ->
    Now = lists:usort([Role | Connected]),
    NextState = case Now of
        [callee, caller] -> active;
        _ -> connecting
    end,
    commit(NextState, Data#{connected := Now}, From, Meta, connecting);
run({connected, _, _}, Role, active, Data, From, Meta) when Role =:= caller; Role =:= callee ->
    commit(active, Data, From, Meta, active);
run({heartbeat, _, _}, Role, connecting, Data, From, Meta) when Role =:= caller; Role =:= callee ->
    commit(connecting, Data, From, Meta, connecting);
run({heartbeat, _, _}, Role, active, Data, From, Meta) when Role =:= caller; Role =:= callee ->
    commit(active, heartbeat(Role, Data), From, Meta#{heartbeat => Role}, active);
run({signal, _, _, Kind}, Role, State, Data, From, Meta) when Role =:= caller; Role =:= callee ->
    case signal(State, Role, Kind, Data) of
        {ok, Next} -> commit(State, Next, From, Meta, State);
        conflict -> {keep_state_and_data, [{reply, From, {error, conflict}}]}
    end;
run(_Command, _Role, _State, _Data, From, _Meta) ->
    {keep_state_and_data, [{reply, From, {error, conflict}}]}.

-spec signal(state_name(), role(), signal_kind(), data()) -> {ok, data()} | conflict.
signal(connecting, caller, offer, #{pending_offer := none} = Data) ->
    {ok, Data#{offered := true, pending_offer := caller}};
signal(connecting, caller, Kind, #{offered := true} = Data)
  when Kind =:= candidate; Kind =:= end_of_candidates -> {ok, Data};
signal(connecting, callee, answer, #{pending_offer := caller} = Data) ->
    {ok, Data#{pending_offer := none}};
signal(connecting, callee, Kind, #{offered := true} = Data)
  when Kind =:= candidate; Kind =:= end_of_candidates -> {ok, Data};
signal(active, Role, offer, #{pending_offer := none} = Data) ->
    {ok, Data#{pending_offer := Role}};
signal(active, caller, answer, #{pending_offer := callee} = Data) ->
    {ok, Data#{pending_offer := none}};
signal(active, callee, answer, #{pending_offer := caller} = Data) ->
    {ok, Data#{pending_offer := none}};
signal(active, _Role, candidate, Data) -> {ok, Data};
signal(_State, _Role, _Kind, _Data) -> conflict.

-spec commit(state_name() | ended, data(), gen_statem:from(), map(), state_name()) ->
    gen_statem:event_handler_result(state_name()).
commit(NextState, NextData0, From, Meta, State) ->
    NextData = change_state(NextData0, NextState),
    Expected = {State, maps:get(state_version, NextData0)},
    case vault_calls_store:persist(vault_calls_store, maps:get(id, NextData0), Expected,
                                   NextData#{command => Meta}) of
        {ok, ended} -> {stop_and_reply, normal, [{reply, From, {ok, ended}}]};
        {ok, NextState} when NextState =:= State ->
            {keep_state, NextData, [{reply, From, {ok, NextState}} | timer_actions(Meta)]};
        {ok, NextState} -> {next_state, NextState, NextData, [{reply, From, {ok, NextState}}]};
        {error, _} = Error -> {keep_state_and_data, [{reply, From, Error}]}
    end.

-spec timer_actions(map()) -> list().
timer_actions(#{heartbeat := Role}) -> [{{timeout, {heartbeat, Role}}, timeout(heartbeat_timeout), missed}];
timer_actions(_Meta) -> [].

-spec change_state(data(), state_name() | ended) -> data().
change_state(#{state := State} = Data, State) ->
    Data;
change_state(#{state_version := Version} = Data, NextState) ->
    Now = erlang:system_time(millisecond),
    Changed = Data#{state := NextState, state_version := Version + 1, state_entered_at := Now},
    case NextState of
        active -> Changed#{caller_heartbeat_at := Now, callee_heartbeat_at := Now};
        _ -> Changed
    end.

-spec heartbeat(role(), data()) -> data().
heartbeat(caller, Data) -> Data#{caller_heartbeat_at := erlang:system_time(millisecond)};
heartbeat(callee, Data) -> Data#{callee_heartbeat_at := erlang:system_time(millisecond)}.

-spec expire(data()) -> gen_statem:event_handler_result(state_name()).
expire(#{id := Id, state_version := Version, caller_device := Device} = Data) ->
    NextData = change_state(Data, ended),
    Expected = {maps:get(state, Data), Version},
    Meta = meta(Id, {timeout, Version}, Device),
    case vault_calls_store:persist(vault_calls_store, Id, Expected, NextData#{command => Meta}) of
        {ok, ended} -> {stop, normal};
        {error, _} -> {keep_state_and_data, [{state_timeout, 1000, expired}]}
    end.

-spec meta(binary(), term(), device() | undefined) -> map().
meta(Id, Command, Device) ->
    CommandId = binary:encode_hex(crypto:strong_rand_bytes(16)),
    #{device => Device, command_id => CommandId,
      fingerprint => crypto:hash(sha256, term_to_binary({Id, Command, CommandId}))}.

-spec command_device(term()) -> device() | undefined.
command_device(Command) when is_tuple(Command), tuple_size(Command) >= 3 -> element(3, Command);
command_device(_Command) -> undefined.

-spec remaining(data(), pos_integer()) -> non_neg_integer().
remaining(#{state_entered_at := Entered}, Timeout) ->
    remaining_since(Entered, Timeout).

-spec heartbeat_remaining(role(), data()) -> non_neg_integer().
heartbeat_remaining(Role, #{state_entered_at := Entered} = Data) ->
    Heartbeat = case Role of
        caller -> maps:get(caller_heartbeat_at, Data);
        callee -> maps:get(callee_heartbeat_at, Data)
    end,
    case Heartbeat > Entered of
        true -> remaining_since(Heartbeat, timeout(heartbeat_timeout));
        false -> remaining_since(Entered, timeout(heartbeat_timeout))
    end.

-spec remaining_since(integer(), pos_integer()) -> non_neg_integer().
remaining_since(Timestamp, Timeout) ->
    Now = erlang:system_time(millisecond),
    trunc(if
        Timestamp > Now -> Timeout;
        Now - Timestamp < Timeout -> Timeout - (Now - Timestamp);
        true -> 0
    end).

-spec timeout(atom()) -> pos_integer().
timeout(Name) ->
    {ok, Value} = application:get_env(vault_calls, Name),
    Value.
