-module(vault_calls_socket).
-moduledoc "Bounded TCP listener for the Calls acceptor pool.".
-behaviour(gen_server).

-export([start_link/3]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-spec start_link(atom(), map(), map()) -> gen_server:start_ret().
start_link(Pool, ListenOpts, AcceptorOpts) ->
    gen_server:start_link(?MODULE, [Pool, ListenOpts, AcceptorOpts], []).

-spec init(list()) -> {ok, {gen_tcp:socket(), reference()}} | {stop, term()}.
init([Pool, ListenOpts, PoolOpts]) ->
    Port = maps:get(port, ListenOpts),
    IPAddress = maps:get(ip, ListenOpts),
    Acceptors = maps:get(size, PoolOpts),
    process_flag(trap_exit, true),
    Options = [{active, false}, {mode, binary}, {packet, raw}, {ip, IPAddress},
               {reuseaddr, true}, {nodelay, true}, {backlog, 200}, {keepalive, true}],
    case gen_tcp:listen(Port, Options) of
        {ok, Socket} ->
            Ref = monitor(port, Socket),
            _ = vault_calls_pool:accept_socket(Pool, Socket, Acceptors),
            {ok, {Socket, Ref}};
        {error, Reason} -> {stop, Reason}
    end.

-spec handle_call(term(), gen_server:from(), term()) -> {stop, term(), term()}.
handle_call(Request, _, State) -> {stop, {bad_call, Request}, State}.

-spec handle_cast(term(), term()) -> {stop, term(), term()}.
handle_cast(Request, State) -> {stop, {bad_cast, Request}, State}.

-spec handle_info(term(), term()) -> {stop, term(), term()} | {noreply, term()}.
handle_info({'DOWN', Ref, port, Socket, Reason}, {Socket, Ref}) -> {stop, Reason, {Socket, Ref}};
handle_info(_, State) -> {noreply, State}.

-spec terminate(term(), {gen_tcp:socket(), reference()}) -> ok.
terminate(_, {Socket, Ref}) ->
    _ = demonitor(Ref, [flush, info]),
    gen_tcp:close(Socket).
