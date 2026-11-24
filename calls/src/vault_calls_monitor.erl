-module(vault_calls_monitor).
-moduledoc "Periodic session and membership checks for every unfinished call.".
-behaviour(gen_server).

-export([start_link/0, check_now/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2]).

-spec start_link() -> gen_server:start_ret().
start_link() -> gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec check_now() -> ok.
check_now() ->
    case whereis(?MODULE) of
        undefined -> ok;
        _ -> gen_server:cast(?MODULE, check_now)
    end.

-spec init([]) -> {ok, []}.
init([]) ->
    self() ! check,
    {ok, []}.

-spec handle_call(term(), gen_server:from(), []) -> {reply, {error, badarg}, []}.
handle_call(_Request, _From, State) -> {reply, {error, badarg}, State}.

-spec handle_cast(term(), []) -> {noreply, []}.
handle_cast(check_now, State) ->
    self() ! check,
    {noreply, State};
handle_cast(_Request, State) -> {noreply, State}.

-spec handle_info(term(), []) -> {noreply, []}.
handle_info(check, State) ->
    case vault_calls_store:list_active(vault_calls_store) of
        {ok, Ids} -> check_all(Ids);
        {error, _} -> ok
    end,
    _ = timer:send_after(application:get_env(vault_calls, monitor_interval, 5000), check),
    {noreply, State};
handle_info(_Message, State) -> {noreply, State}.

-spec check_all([binary()]) -> ok.
check_all(Ids) ->
    Monitors = [begin
        {Pid, Ref} = spawn_monitor(fun() -> check(Id) end),
        {Ref, Pid}
    end || Id <- Ids],
    collect(Monitors, 10000).

-spec collect([{reference(), pid()}], non_neg_integer()) -> ok.
collect([], _Timeout) -> ok;
collect(Monitors, Timeout) ->
    receive
        {'DOWN', Ref, process, _Pid, _Reason} ->
            case lists:keytake(Ref, 1, Monitors) of
                {value, {Ref, _}, Rest} -> collect(Rest, Timeout);
                false -> collect(Monitors, Timeout)
            end
    after Timeout ->
        [erlang:demonitor(Ref, [flush]) || {Ref, _} <- Monitors],
        ok
    end.

-spec check(binary()) -> ok.
check(Id) ->
    case vault_calls_store:get_call(vault_calls_store, Id) of
        {ok, Data} -> check_call(Data);
        {error, _} -> ok
    end.

-spec check_call(map()) -> ok.
check_call(Data) ->
    Caller = maps:get(caller, Data),
    CallerDevice = maps:get(caller_device, Data),
    case (auth_mod()):validate_session(Caller, CallerDevice) of
        {error, revoked} -> end_call(Data);
        {error, unavailable} -> ok;
        ok -> check_callee_session(Data)
    end.

-spec check_callee_session(map()) -> ok.
check_callee_session(Data) ->
    case maps:get(callee_device, Data) of
        undefined -> check_memberships(Data);
        Device ->
            Callee = maps:get(callee, Data),
            case (auth_mod()):validate_session(Callee, Device) of
                {error, revoked} -> end_call(Data);
                {error, unavailable} -> ok;
                ok -> check_memberships(Data)
            end
    end.

-spec check_memberships(map()) -> ok.
check_memberships(Data) ->
    Room = maps:get(room_id, Data),
    case (chat_mod()):check_membership(Room, maps:get(caller, Data)) of
        {error, removed} -> end_call(Data);
        {error, unavailable} -> ok;
        ok ->
            case (chat_mod()):check_membership(Room, maps:get(callee, Data)) of
                {error, removed} -> end_call(Data);
                _ -> ok
            end
    end.

-spec end_call(map()) -> ok.
end_call(Data) ->
    Id = maps:get(id, Data),
    Device = maps:get(caller_device, Data),
    CommandId = binary:encode_hex(crypto:strong_rand_bytes(16)),
    Meta = #{device => Device, command_id => CommandId,
             fingerprint => crypto:hash(sha256, term_to_binary({monitor_end, Id, CommandId}))},
    _ = vault_call:command(Id, {'end', maps:get(caller, Data), Device}, Meta),
    ok.

-spec auth_mod() -> module().
auth_mod() -> application:get_env(vault_calls, auth_mod, vault_calls_auth_stub).

-spec chat_mod() -> module().
chat_mod() -> application:get_env(vault_calls, chat_mod, vault_calls_chat_stub).
