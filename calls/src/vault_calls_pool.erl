-module(vault_calls_pool).
-moduledoc "Acceptor pool that preserves the configured TLS version and identity checks.".
-behaviour(acceptor_pool).

-export([start_link/4, accept_socket/3, init/1]).

-spec start_link(atom(), term(), term(), map()) -> {ok, pid()} | ignore | {error, term()}.
start_link(Name, ServerOpts, ChatterboxOpts, TransportOpts) ->
    acceptor_pool:start_link({local, Name}, ?MODULE, [ServerOpts, ChatterboxOpts, TransportOpts]).

-spec accept_socket(atom(), gen_tcp:socket(), non_neg_integer()) -> term().
accept_socket(Pool, Socket, Acceptors) ->
    acceptor_pool:accept_socket(Pool, Socket, Acceptors).

-spec init(list()) -> {ok, {map(), [supervisor:child_spec()]}}.
init([ServerOpts, ChatterboxOpts, TransportOpts]) ->
    {Transport, SslOpts} = case TransportOpts of
        #{ssl := true} ->
            {ssl, [{alpn_preferred_protocols, [<<"h2">>]} | maps:to_list(maps:remove(ssl, TransportOpts))]};
        _ -> {gen_tcp, []}
    end,
    Conn = #{id => vault_calls_acceptor,
             start => {grpcbox_acceptor,
                 {Transport, chatterbox:settings(server, ServerOpts), ChatterboxOpts, SslOpts}, []},
             grace => 5000},
    {ok, {#{}, [Conn]}}.
