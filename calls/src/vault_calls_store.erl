-module(vault_calls_store).
-moduledoc "PostgreSQL storage and admission for the Calls prototype.".
-behaviour(gen_server).

-export([start_link/1, stop/1, connect/1, migrate/1, create_call/2]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-type create_args() :: #{id := binary(), room_id := binary(), caller := binary(),
                         caller_device := binary(), callee := binary(), command_id := binary(),
                         fingerprint := binary()}.
-type create_reply() :: {ok, map()} | {error, busy | capacity | command_id_reused | badarg | unavailable}.
-export_type([create_args/0, create_reply/0]).

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
handle_call({create_call, _Args}, _From, Connection) ->
    {reply, {error, not_implemented}, Connection};
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

-spec connection_options(binary()) -> {ok, epgsql:connect_opts()} | error.
connection_options(Url) ->
    try uri_string:parse(Url) of
        #{scheme := <<"postgres">>, host := Host, userinfo := Userinfo, path := <<$/, Database/binary>>} = Parsed
          when Host =/= <<>>, Database =/= <<>> ->
            case {binary:split(Userinfo, <<":">>), maps:get(query, Parsed, <<>>)} of
                {[Username, Password], <<"sslmode=disable">>} ->
                    {ok, #{host => binary_to_list(Host), username => Username, password => Password, database => Database,
                           port => maps:get(port, Parsed, 5432), timeout => 2000, ssl => false,
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
