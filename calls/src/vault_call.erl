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
`conflict`. The process ends with the call. State lives only here until
persistence comes.
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
-type start_args() :: #{id := binary(), caller := user(), caller_device := device(), callee := user()}.
-type command() :: {accept | reject | cancel | connected | 'end' | heartbeat, user(), device()}
                 | {signal, user(), device(), signal_kind()}.
-type reply() :: {ok, state_name() | ended} | {error, not_found | conflict | badarg}.
-type data() :: #{id := binary(), caller := user(), caller_device := device(), callee := user(),
                  callee_device := device() | undefined, connected := [role()], offered := boolean()}.

-spec start_link(start_args()) -> gen_statem:start_ret().
start_link(#{id := Id} = Args) ->
    gen_statem:start_link({via, vault_calls_registry, Id}, ?MODULE, Args, []).

-doc "Runs a command; an unknown or ended call is `not_found`.".
-spec command(binary(), command()) -> reply().
command(Id, Command) ->
    try gen_statem:call({via, vault_calls_registry, Id}, Command, 5000)
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

-spec init(start_args()) -> gen_statem:init_result(state_name()).
init(#{id := Id, caller := Caller, caller_device := Device, callee := Callee}) when Caller =/= Callee ->
    {ok, ringing, #{id => Id, caller => Caller, caller_device => Device, callee => Callee,
                    callee_device => undefined, connected => [], offered => false}}.

-spec handle_event(gen_statem:event_type(), term(), state_name(), data()) ->
    gen_statem:event_handler_result(state_name()).
handle_event(enter, _Old, ringing, _Data) ->
    {keep_state_and_data, [{state_timeout, timeout(ringing_timeout), expired}]};
handle_event(enter, _Old, connecting, _Data) ->
    {keep_state_and_data, [{state_timeout, timeout(connecting_timeout), expired}]};
handle_event(enter, _Old, active, _Data) ->
    Heartbeat = timeout(heartbeat_timeout),
    {keep_state_and_data, [{state_timeout, timeout(max_call_duration), expired},
                           {{timeout, {heartbeat, caller}}, Heartbeat, missed},
                           {{timeout, {heartbeat, callee}}, Heartbeat, missed}]};
handle_event(state_timeout, expired, _State, _Data) ->
    {stop, normal};
handle_event({timeout, {heartbeat, _Role}}, missed, active, _Data) ->
    {stop, normal};
handle_event({call, From}, get_state, State, _Data) ->
    {keep_state_and_data, [{reply, From, {ok, State}}]};
handle_event({call, From}, Command, State, Data) when tuple_size(Command) >= 3 ->
    case role(element(2, Command), element(3, Command), Data) of
        stranger -> {keep_state_and_data, [{reply, From, {error, not_found}}]};
        Role -> run(Command, Role, State, Data, From)
    end;
handle_event({call, From}, _Malformed, _State, _Data) ->
    {keep_state_and_data, [{reply, From, {error, badarg}}]};
handle_event(info, _Message, _State, _Data) ->
    keep_state_and_data.

%% role/3 tells the caller's device, the callee's device (any of them before
%% accept), other devices of the participants, and strangers apart.
-spec role(user(), device(), data()) -> role() | other_device | stranger.
role(Caller, Device, #{caller := Caller, caller_device := Device}) -> caller;
role(Caller, _Device, #{caller := Caller}) -> other_device;
role(Callee, _Device, #{callee := Callee, callee_device := undefined}) -> callee;
role(Callee, Device, #{callee := Callee, callee_device := Device}) -> callee;
role(Callee, _Device, #{callee := Callee}) -> other_device;
role(_User, _Device, _Data) -> stranger.

-spec run(command(), role() | other_device, state_name(), data(), gen_statem:from()) ->
    gen_statem:event_handler_result(state_name()).
run({accept, _, Device}, callee, ringing, Data, From) ->
    {next_state, connecting, Data#{callee_device := Device}, [{reply, From, {ok, connecting}}]};
run({reject, _, _}, callee, ringing, _Data, From) ->
    finish(From);
run({cancel, _, _}, caller, ringing, _Data, From) ->
    finish(From);
run({'end', _, _}, Role, _State, _Data, From) when Role =:= caller; Role =:= callee ->
    finish(From);
run({connected, _, _}, Role, connecting, #{connected := Connected} = Data, From)
  when Role =:= caller; Role =:= callee ->
    case lists:usort([Role | Connected]) of
        [callee, caller] -> {next_state, active, Data#{connected := [callee, caller]}, [{reply, From, {ok, active}}]};
        Now -> {keep_state, Data#{connected := Now}, [{reply, From, {ok, connecting}}]}
    end;
run({connected, _, _}, Role, active, _Data, From) when Role =:= caller; Role =:= callee ->
    {keep_state_and_data, [{reply, From, {ok, active}}]};
run({heartbeat, _, _}, Role, connecting, _Data, From) when Role =:= caller; Role =:= callee ->
    {keep_state_and_data, [{reply, From, {ok, connecting}}]};
run({heartbeat, _, _}, Role, active, _Data, From) when Role =:= caller; Role =:= callee ->
    {keep_state_and_data, [{{timeout, {heartbeat, Role}}, timeout(heartbeat_timeout), missed},
                           {reply, From, {ok, active}}]};
run({signal, _, _, Kind}, Role, State, Data, From) when Role =:= caller; Role =:= callee ->
    case signal(State, Role, Kind, Data) of
        {ok, Next} -> {keep_state, Next, [{reply, From, {ok, State}}]};
        conflict -> {keep_state_and_data, [{reply, From, {error, conflict}}]}
    end;
run(_Command, _Role, _State, _Data, From) ->
    {keep_state_and_data, [{reply, From, {error, conflict}}]}.

%% signal/4 is the signal matrix of the ADR. While connecting, the caller
%% offers first and the callee answers only an offer; nobody signals before
%% accept, so the caller learns nothing about a callee who did not answer.
-spec signal(state_name(), role(), signal_kind(), data()) -> {ok, data()} | conflict.
signal(connecting, caller, offer, Data) -> {ok, Data#{offered := true}};
signal(connecting, caller, Kind, #{offered := true} = Data)
  when Kind =:= candidate; Kind =:= end_of_candidates -> {ok, Data};
signal(connecting, callee, Kind, #{offered := true} = Data)
  when Kind =:= answer; Kind =:= candidate; Kind =:= end_of_candidates -> {ok, Data};
signal(active, _Role, Kind, Data) when Kind =:= offer; Kind =:= answer; Kind =:= candidate -> {ok, Data};
signal(_State, _Role, _Kind, _Data) -> conflict.

-spec finish(gen_statem:from()) -> gen_statem:event_handler_result(state_name()).
finish(From) ->
    {stop_and_reply, normal, [{reply, From, {ok, ended}}]}.

-spec timeout(atom()) -> pos_integer().
timeout(Name) ->
    {ok, Value} = application:get_env(vault_calls, Name),
    Value.
