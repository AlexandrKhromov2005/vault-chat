-module(vault_calls_store).
-moduledoc "PostgreSQL storage and admission for the Calls prototype.".
-behaviour(gen_server).

-include_lib("epgsql/include/epgsql.hrl").

-export([start_link/0, start_link/1, stop/1, connect/1, migrate/1, create_call/2]).
-export([get_call/2, list_active/1, persist/4, replay/2]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-type create_args() :: #{id := binary(), room_id := binary(), caller := binary(),
                         caller_device := binary(), callee := binary(), command_id := binary(),
                         fingerprint := binary()}.
-type command_meta() :: #{device := binary(), command_id := binary(), fingerprint := binary()}.
-type create_reply() :: {ok, map()} | {error, busy | capacity | command_id_reused | badarg | unavailable}.
-type persist_reply() :: {ok, atom()} | {error, not_found | conflict | command_id_reused | badarg | unavailable}.
-export_type([create_args/0, create_reply/0, command_meta/0]).

-spec start_link() -> gen_server:start_ret().
start_link() ->
    {ok, Url} = application:get_env(vault_calls, database_url),
    gen_server:start_link({local, ?MODULE}, ?MODULE, Url, []).

-spec start_link(binary()) -> gen_server:start_ret().
start_link(Url) ->
    gen_server:start_link(?MODULE, Url, []).

-spec stop(gen_server:server_ref()) -> ok.
stop(Store) ->
    gen_server:stop(Store).

-spec migrate(gen_server:server_ref()) -> ok | {error, unavailable}.
migrate(Store) ->
    request(Store, migrate, 5000).

-spec create_call(gen_server:server_ref(), create_args()) -> create_reply().
create_call(Store, Args) ->
    request(Store, {create_call, Args}, 2500).

-spec get_call(gen_server:server_ref(), binary()) -> {ok, map()} | {error, not_found | unavailable}.
get_call(Store, Id) ->
    request(Store, {get_call, Id}, 2500).

-spec list_active(gen_server:server_ref()) -> {ok, [binary()]} | {error, unavailable}.
list_active(Store) ->
    request(Store, list_active, 2500).

-spec persist(gen_server:server_ref(), binary(), {atom(), non_neg_integer()}, map()) -> persist_reply().
persist(Store, Id, Expected, Next) ->
    request(Store, {persist, Id, Expected, Next}, 2500).

-spec replay(gen_server:server_ref(), command_meta()) -> persist_reply().
replay(Store, Meta) ->
    request(Store, {replay, Meta}, 2500).

-spec connect(binary()) -> {ok, epgsql:connection()} | {error, badarg | unavailable}.
connect(Url) ->
    case connection_options(Url) of
        {ok, Options} ->
            case epgsql:connect(Options) of
                {ok, Connection} ->
                    case epgsql:squery(Connection, "SET statement_timeout = 2000; SET lock_timeout = 1500") of
                        [{ok, [], []}, {ok, [], []}] -> {ok, Connection};
                        _ -> close(Connection), {error, unavailable}
                    end;
                {error, _} -> {error, unavailable}
            end;
        error -> {error, badarg}
    end.

-spec init(binary()) -> {ok, epgsql:connection()} | {stop, badarg | unavailable}.
init(Url) ->
    case connect(Url) of
        {ok, Connection} -> {ok, Connection};
        {error, Reason} -> {stop, Reason}
    end.

-spec handle_call(term(), gen_server:from(), epgsql:connection()) ->
    {reply, term(), epgsql:connection()}.
handle_call(migrate, _From, Connection) ->
    {reply, migrate_database(Connection), Connection};
handle_call({create_call, Args}, _From, Connection) ->
    {reply, safe(fun() -> create(Connection, Args) end), Connection};
handle_call({get_call, Id}, _From, Connection) ->
    {reply, safe(fun() -> get(Connection, Id) end), Connection};
handle_call(list_active, _From, Connection) ->
    {reply, safe(fun() -> active(Connection) end), Connection};
handle_call({persist, Id, Expected, Next}, _From, Connection) ->
    {reply, safe(fun() -> save(Connection, Id, Expected, Next) end), Connection};
handle_call({replay, Meta}, _From, Connection) ->
    {reply, safe(fun() -> replay_saved(Connection, Meta) end), Connection};
handle_call(_Request, _From, Connection) ->
    {reply, {error, badarg}, Connection}.

-spec handle_cast(term(), epgsql:connection()) -> {noreply, epgsql:connection()}.
handle_cast(_Request, Connection) ->
    {noreply, Connection}.

-spec handle_info(term(), epgsql:connection()) -> {noreply, epgsql:connection()}.
handle_info(_Message, Connection) ->
    {noreply, Connection}.

-spec terminate(term(), epgsql:connection()) -> ok.
terminate(_Reason, Connection) ->
    close(Connection).

-spec replay_saved(epgsql:connection(), term()) -> persist_reply().
replay_saved(Connection, #{device := Device, command_id := CommandId, fingerprint := Fingerprint})
  when is_binary(Device), byte_size(Device) > 0, byte_size(Device) =< 256,
       is_binary(CommandId), byte_size(CommandId) > 0, byte_size(CommandId) =< 128,
       is_binary(Fingerprint), byte_size(Fingerprint) =:= 32 ->
    case query(Connection, "SELECT fingerprint, response FROM call_commands WHERE device = $1 AND command_id = $2",
               [Device, CommandId]) of
        {ok, _, [{Fingerprint, Response}]} ->
            #{<<"state">> := State} = json:decode(Response),
            {ok, state_from_binary(State)};
        {ok, _, [{_, _}]} -> {error, command_id_reused};
        {ok, _, []} -> {error, not_found}
    end;
replay_saved(_Connection, _Meta) ->
    {error, badarg}.

-spec create(epgsql:connection(), term()) -> create_reply().
create(Connection, #{id := Id, room_id := Room, caller := Caller, caller_device := Device,
                     callee := Callee, command_id := CommandId, fingerprint := Fingerprint})
  when is_binary(Id), byte_size(Id) > 0, byte_size(Id) =< 128,
       is_binary(Room), byte_size(Room) > 0, byte_size(Room) =< 128,
       is_binary(Caller), byte_size(Caller) > 0, byte_size(Caller) =< 256,
       is_binary(Device), byte_size(Device) > 0, byte_size(Device) =< 256,
       is_binary(Callee), byte_size(Callee) > 0, byte_size(Callee) =< 256,
       Caller =/= Callee, is_binary(CommandId), byte_size(CommandId) > 0,
       byte_size(CommandId) =< 128, is_binary(Fingerprint), byte_size(Fingerprint) =:= 32 ->
    Args = {Id, Room, Caller, Device, Callee, CommandId, Fingerprint},
    case epgsql:with_transaction(Connection, fun(C) -> create_transaction(C, Args) end,
                                 #{ensure_committed => true, reraise => false}) of
        {rollback, {abort, Reason}} -> {error, Reason};
        {rollback, _} -> {error, unavailable};
        Reply -> Reply
    end;
create(_Connection, _Args) ->
    {error, badarg}.

-spec safe(fun(() -> term())) -> term().
safe(Fun) ->
    try Fun() of
        Result -> Result
    catch
        _:_ -> {error, unavailable}
    end.

-spec get(epgsql:connection(), binary()) -> {ok, map()} | {error, not_found | badarg}.
get(Connection, Id) when is_binary(Id), byte_size(Id) > 0, byte_size(Id) =< 128 ->
    case query(Connection,
        "SELECT id, room_id, state, state_version, caller_id, caller_device, callee_id, "
        "coalesce(callee_device, ''), connected_caller, connected_callee, offered, coalesce(pending_offer, ''), "
        "floor(extract(epoch from state_entered_at) * 1000)::bigint, "
        "coalesce(floor(extract(epoch from caller_heartbeat_at) * 1000)::bigint, 0), "
        "coalesce(floor(extract(epoch from callee_heartbeat_at) * 1000)::bigint, 0) "
        "FROM calls WHERE id = $1 AND state <> 'ended'", [Id]) of
        {ok, _, [{Id, Room, State, Version, Caller, Device, Callee, CalleeDevice,
                  ConnectedCaller, ConnectedCallee, Offered, Pending, Entered, CallerHeartbeat, CalleeHeartbeat}]} ->
            {ok, #{id => Id, room_id => Room, state => state_from_binary(State), state_version => Version,
                   caller => Caller, caller_device => Device, callee => Callee,
                   callee_device => optional_binary(CalleeDevice),
                   connected => connected_roles(ConnectedCaller, ConnectedCallee), offered => Offered,
                   pending_offer => optional_role(Pending), state_entered_at => Entered,
                   caller_heartbeat_at => CallerHeartbeat, callee_heartbeat_at => CalleeHeartbeat}};
        {ok, _, []} -> {error, not_found}
    end;
get(_Connection, _Id) ->
    {error, badarg}.

-spec active(epgsql:connection()) -> {ok, [binary()]}.
active(Connection) ->
    {ok, _, Rows} = query(Connection,
        "SELECT id FROM calls WHERE state <> 'ended' ORDER BY created_at LIMIT 200", []),
    {ok, [Id || {Id} <- Rows]}.

-spec save(epgsql:connection(), binary(), {atom(), non_neg_integer()}, map()) -> persist_reply().
save(Connection, Id, {ExpectedState, ExpectedVersion},
     #{command := #{device := Device, command_id := CommandId, fingerprint := Fingerprint},
       state := NextState, state_version := NextVersion, callee_device := _,
       connected := Connected, offered := Offered, pending_offer := Pending,
       state_entered_at := Entered, caller_heartbeat_at := CallerHeartbeat,
       callee_heartbeat_at := CalleeHeartbeat} = Next)
  when is_binary(Id), byte_size(Id) > 0, byte_size(Id) =< 128,
       is_atom(ExpectedState), is_integer(ExpectedVersion), ExpectedVersion >= 0,
       is_atom(NextState), is_integer(NextVersion), NextVersion >= ExpectedVersion,
       is_binary(Device), byte_size(Device) > 0, byte_size(Device) =< 256,
       is_binary(CommandId), byte_size(CommandId) > 0, byte_size(CommandId) =< 128,
       is_binary(Fingerprint), byte_size(Fingerprint) =:= 32,
       is_list(Connected), is_boolean(Offered), is_atom(Pending),
       is_integer(Entered), Entered > 0, is_integer(CallerHeartbeat), is_integer(CalleeHeartbeat) ->
    Args = {Id, ExpectedState, ExpectedVersion, Device, CommandId, Fingerprint, Next},
    case epgsql:with_transaction(Connection, fun(C) -> save_transaction(C, Args) end,
                                 #{ensure_committed => true, reraise => false}) of
        {rollback, {abort, Reason}} -> {error, Reason};
        {rollback, _} -> {error, unavailable};
        Reply -> Reply
    end;
save(_Connection, _Id, _Expected, _Next) ->
    {error, badarg}.

-spec save_transaction(epgsql:connection(), tuple()) -> {ok, atom()} | {error, not_found | conflict}.
save_transaction(Connection, {Id, ExpectedState, ExpectedVersion, Device, CommandId, Fingerprint, Next}) ->
    _ = query(Connection, "SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))", [Device, CommandId]),
    case query(Connection, "SELECT fingerprint, response FROM call_commands WHERE device = $1 AND command_id = $2",
               [Device, CommandId]) of
        {ok, _, [{Fingerprint, Response}]} ->
            #{<<"state">> := State} = json:decode(Response),
            {ok, state_from_binary(State)};
        {ok, _, [{_, _}]} -> abort(command_id_reused);
        {ok, _, []} ->
            case query(Connection, "SELECT state, state_version FROM calls WHERE id = $1 FOR UPDATE", [Id]) of
                {ok, _, [{State, Version}]} ->
                    case {state_from_binary(State), Version} of
                        {ExpectedState, ExpectedVersion} ->
                            persist_row(Connection, Id, ExpectedState, ExpectedVersion,
                                        Device, CommandId, Fingerprint, Next);
                        _ -> {error, conflict}
                    end;
                {ok, _, []} -> {error, not_found}
            end
    end.

-spec persist_row(epgsql:connection(), binary(), atom(), non_neg_integer(), binary(), binary(), binary(), map()) ->
    {ok, atom()}.
persist_row(Connection, Id, ExpectedState, ExpectedVersion, Device, CommandId, Fingerprint,
            #{state := NextState, state_version := NextVersion, callee_device := CalleeDevice,
              connected := Connected, offered := Offered, pending_offer := Pending,
              state_entered_at := Entered, caller_heartbeat_at := CallerHeartbeat,
              callee_heartbeat_at := CalleeHeartbeat}) ->
    NextStateBinary = atom_to_binary(NextState),
    {ok, 1} = query(Connection,
        "UPDATE calls SET state = $2, state_version = $3, callee_device = $4, "
        "connected_caller = $5, connected_callee = $6, offered = $7, pending_offer = $8, "
        "state_entered_at = to_timestamp($9 / 1000.0), "
        "caller_heartbeat_at = case when $10::bigint = 0 then null else to_timestamp($10::bigint / 1000.0) end, "
        "callee_heartbeat_at = case when $11::bigint = 0 then null else to_timestamp($11::bigint / 1000.0) end, "
        "ended_at = case when $2 = 'ended' then clock_timestamp() else null end "
        "WHERE id = $1 AND state = $12 AND state_version = $13",
        [Id, NextStateBinary, NextVersion, CalleeDevice, lists:member(caller, Connected),
         lists:member(callee, Connected), Offered, nullable_role(Pending), Entered, CallerHeartbeat, CalleeHeartbeat,
         atom_to_binary(ExpectedState), ExpectedVersion]),
    Encoded = iolist_to_binary(json:encode(#{<<"state">> => NextStateBinary})),
    {ok, 1} = query(Connection,
        "INSERT INTO call_commands (device, command_id, call_id, fingerprint, response) "
        "VALUES ($1, $2, $3, $4, $5)", [Device, CommandId, Id, Fingerprint, Encoded]),
    NextState =:= ended andalso release(Connection, Id),
    {ok, NextState}.

-spec release(epgsql:connection(), binary()) -> ok.
release(Connection, Id) ->
    {ok, _} = query(Connection, "DELETE FROM call_participants WHERE call_id = $1", [Id]),
    {ok, 1} = query(Connection,
        "UPDATE call_capacity SET active_calls = active_calls - 1 WHERE id = 1 AND active_calls > 0", []),
    ok.

-spec optional_binary(binary()) -> binary() | undefined.
optional_binary(<<>>) -> undefined;
optional_binary(Value) -> Value.

-spec optional_role(binary()) -> none | caller | callee.
optional_role(<<>>) -> none;
optional_role(<<"caller">>) -> caller;
optional_role(<<"callee">>) -> callee.

-spec nullable_role(none | caller | callee) -> null | caller | callee.
nullable_role(none) -> null;
nullable_role(Role) -> Role.

-spec connected_roles(boolean(), boolean()) -> [caller | callee].
connected_roles(Caller, Callee) ->
    [callee || Callee] ++ [caller || Caller].

-spec state_from_binary(binary()) -> atom().
state_from_binary(<<"ringing">>) -> ringing;
state_from_binary(<<"connecting">>) -> connecting;
state_from_binary(<<"active">>) -> active;
state_from_binary(<<"ended">>) -> ended;
state_from_binary(<<"caller">>) -> caller;
state_from_binary(<<"callee">>) -> callee.

-spec create_transaction(epgsql:connection(), tuple()) -> {ok, map()}.
create_transaction(Connection, {Id, Room, Caller, Device, Callee, CommandId, Fingerprint}) ->
    _ = query(Connection, "SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))", [Device, CommandId]),
    case query(Connection, "SELECT fingerprint, response FROM call_commands WHERE device = $1 AND command_id = $2",
               [Device, CommandId]) of
        {ok, _, [{Fingerprint, Response}]} ->
            {ok, json:decode(Response)};
        {ok, _, [{_, _}]} ->
            abort(command_id_reused);
        {ok, _, []} ->
            insert_participants(Connection, Id, Caller, Callee),
            {ok, _, [{Active, Limit}]} = query(Connection,
                "SELECT active_calls, capacity_limit FROM call_capacity WHERE id = 1 FOR UPDATE", []),
            Active < Limit orelse abort(capacity),
            {ok, 1} = query(Connection,
                "INSERT INTO calls (id, room_id, caller_id, caller_device, callee_id) VALUES ($1, $2, $3, $4, $5)",
                [Id, Room, Caller, Device, Callee]),
            Body = #{<<"call_id">> => Id, <<"state">> => <<"ringing">>},
            Encoded = iolist_to_binary(json:encode(Body)),
            {ok, 1} = query(Connection,
                "INSERT INTO call_commands (device, command_id, call_id, fingerprint, response) "
                "VALUES ($1, $2, $3, $4, $5)",
                [Device, CommandId, Id, Fingerprint, Encoded]),
            {ok, 1} = query(Connection, "UPDATE call_capacity SET active_calls = active_calls + 1 WHERE id = 1", []),
            {ok, Body}
    end.

-spec insert_participants(epgsql:connection(), binary(), binary(), binary()) -> ok.
insert_participants(Connection, Id, Caller, Callee) ->
    [insert_participant(Connection, Id, User) || User <- lists:sort([Caller, Callee])],
    ok.

-spec insert_participant(epgsql:connection(), binary(), binary()) -> ok.
insert_participant(Connection, Id, User) ->
    case epgsql:equery(Connection, "INSERT INTO call_participants (user_id, call_id) VALUES ($1, $2)", [User, Id]) of
        {ok, 1} -> ok;
        {error, #error{code = <<"23505">>}} -> abort(busy);
        {error, _} -> abort(unavailable)
    end.

-spec query(epgsql:connection(), iodata(), list()) -> term().
query(Connection, Sql, Params) ->
    case epgsql:equery(Connection, Sql, Params) of
        {error, _} -> abort(unavailable);
        Result -> Result
    end.

-spec abort(atom()) -> no_return().
abort(Reason) ->
    throw({abort, Reason}).

-spec request(gen_server:server_ref(), term(), timeout()) -> term().
request(Store, Request, Timeout) ->
    try gen_server:call(Store, Request, Timeout)
    catch exit:_ -> {error, unavailable}
    end.

-spec migrate_database(epgsql:connection()) -> ok | {error, unavailable}.
migrate_database(Connection) ->
    Path = filename:join(code:priv_dir(vault_calls), "migrations/0001_init.sql"),
    case file:read_file(Path) of
        {ok, Sql} -> check_results(epgsql:squery(Connection, Sql));
        {error, _} -> {error, unavailable}
    end.

-spec check_results(term()) -> ok | {error, unavailable}.
check_results(Results) when is_list(Results) ->
    case lists:all(fun(Result) -> check_results(Result) =:= ok end, Results) of
        true -> ok;
        false -> {error, unavailable}
    end;
check_results({error, _}) -> {error, unavailable};
check_results(_) -> ok.

-spec connection_options(binary()) -> {ok, map()} | error.
connection_options(Url) ->
    try uri_string:parse(Url) of
        #{scheme := <<"postgres">>, host := Host, userinfo := Userinfo, path := <<$/, Database/binary>>} = Parsed
          when Host =/= <<>>, Database =/= <<>> ->
            case {binary:split(Userinfo, <<":">>), maps:get(query, Parsed, <<>>)} of
                {[Username, Password], <<"sslmode=disable">>} ->
                    {ok, #{host => binary_to_list(Host), username => binary_to_list(Username), password => Password,
                           database => binary_to_list(Database), port => maps:get(port, Parsed, 5432), timeout => 2000, ssl => false,
                           socket_active => 128}};
                _ -> error
            end;
        _ -> error
    catch
        error:_ -> error
    end.

-spec close(epgsql:connection()) -> ok.
close(Connection) ->
    _ = epgsql:close(Connection),
    ok.
