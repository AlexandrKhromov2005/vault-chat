-module(vault_calls_subchannel).
-moduledoc "TLS 1.3 ALPN-compatible grpcbox subchannel.".
-behaviour(gen_statem).

-export([start_link/5, conn/1, conn/2, stop/2]).
-export([init/1, callback_mode/0, terminate/3, ready/3, disconnected/3]).

-record(data, {endpoint :: vault_calls_channel:endpoint(),
               channel :: vault_calls_channel:t(),
               info :: #{authority := binary(), scheme := binary(),
                         encoding := grpcbox:encoding(), stats_handler := module() | undefined},
               conn :: chatterbox_h2_stream_set:stream_set() | undefined,
               conn_pid :: pid() | undefined}).

-spec start_link(term(), atom(), vault_calls_channel:endpoint(), grpcbox:encoding(), module() | undefined) ->
    gen_statem:start_ret().
start_link(Name, Channel, Endpoint, Encoding, StatsHandler) ->
    gen_statem:start_link(?MODULE, [Name, Channel, Endpoint, Encoding, StatsHandler], []).

-spec conn(pid()) -> term().
conn(Pid) -> conn(Pid, infinity).

-spec conn(pid(), timeout()) -> term().
conn(Pid, Timeout) ->
    try gen_statem:call(Pid, conn, Timeout)
    catch exit:{timeout, _} -> {error, timeout}
    end.

-spec stop(pid(), term()) -> ok.
stop(Pid, Reason) -> gen_statem:stop(Pid, Reason, infinity).

-spec init(list()) -> {ok, disconnected, #data{}}.
init([Name, Channel, Endpoint, Encoding, StatsHandler]) ->
    process_flag(trap_exit, true),
    gproc_pool:connect_worker(Channel, Name),
    {ok, disconnected, #data{conn = undefined, info = info_map(Endpoint, Encoding, StatsHandler),
                             endpoint = Endpoint, channel = Channel}}.

-spec info_map(vault_calls_channel:endpoint(), grpcbox:encoding(), module() | undefined) -> map().
info_map({Scheme, {local, _UnixPath} = Host, Port, _, _}, Encoding, StatsHandler) ->
    case {Scheme, Port} of
        {http, 0} ->
            #{authority => <<"localhost">>, scheme => <<"http">>,
              encoding => Encoding, stats_handler => StatsHandler};
        _ -> error({badarg, [Scheme, Host, Port]})
    end;
info_map({http, Host, 80, _, _}, Encoding, StatsHandler) ->
    #{authority => list_to_binary(Host), scheme => <<"http">>,
      encoding => Encoding, stats_handler => StatsHandler};
info_map({https, Host, 443, _, _}, Encoding, StatsHandler) ->
    #{authority => list_to_binary(Host), scheme => <<"https">>,
      encoding => Encoding, stats_handler => StatsHandler};
info_map({Scheme, Host, Port, _, _}, Encoding, StatsHandler) ->
    #{authority => list_to_binary(Host ++ ":" ++ integer_to_list(Port)),
      scheme => atom_to_binary(Scheme, utf8), encoding => Encoding, stats_handler => StatsHandler}.

-spec callback_mode() -> state_functions.
callback_mode() -> state_functions.

-spec ready(gen_statem:event_type(), term(), #data{}) -> gen_statem:event_handler_result(disconnected | ready).
ready({call, From}, conn, #data{conn = Conn, info = Info}) ->
    {keep_state_and_data, [{reply, From, {ok, Conn, Info}}]};
ready(EventType, EventContent, Data) -> handle_event(EventType, EventContent, Data).

-spec disconnected(gen_statem:event_type(), term(), #data{}) -> gen_statem:event_handler_result(disconnected | ready).
disconnected({call, From}, conn, Data) -> connect(Data, From, [postpone]);
disconnected(EventType, EventContent, Data) -> handle_event(EventType, EventContent, Data).

-spec handle_event(gen_statem:event_type(), term(), #data{}) -> gen_statem:event_handler_result(disconnected | ready).
handle_event({call, From}, info, #data{info = Info}) ->
    {keep_state_and_data, [{reply, From, Info}]};
handle_event(info, {'EXIT', Pid, _}, Data = #data{conn_pid = Pid}) ->
    {next_state, disconnected, Data#data{conn = undefined, conn_pid = undefined}};
handle_event(info, {'EXIT', _, econnrefused}, #data{conn = undefined, conn_pid = undefined}) ->
    keep_state_and_data;
handle_event({call, From}, shutdown, _) ->
    {stop_and_reply, normal, {reply, From, ok}};
handle_event(_, _, _) -> keep_state_and_data.

-spec terminate(term(), atom(), #data{}) -> ok.
terminate(_Reason, _State, #data{conn = undefined, endpoint = Endpoint, channel = Channel}) ->
    gproc_pool:disconnect_worker(Channel, Endpoint),
    gproc_pool:remove_worker(Channel, Endpoint),
    ok;
terminate(normal, _State, #data{conn = Pid, endpoint = Endpoint, channel = Channel}) ->
    chatterbox_h2_connection:stop(Pid),
    gproc_pool:disconnect_worker(Channel, Endpoint),
    gproc_pool:remove_worker(Channel, Endpoint),
    ok;
terminate(Reason, _State, #data{conn_pid = Pid, endpoint = Endpoint, channel = Channel}) ->
    exit(Pid, Reason),
    gproc_pool:disconnect_worker(Channel, Endpoint),
    gproc_pool:remove_worker(Channel, Endpoint),
    ok.

-spec connect(#data{}, gen_statem:from(), list()) -> gen_statem:event_handler_result(disconnected | ready).
connect(Data = #data{conn = undefined,
                     endpoint = {Transport, Host, Port, SSLOptions, ConnectionSettings}}, From, Actions) ->
    case chatterbox_h2_client:start_link(Transport, Host, Port, options(Transport, SSLOptions),
        ConnectionSettings#{garbage_on_end => true, stream_callback_mod => grpcbox_client_stream}) of
        {ok, Conn} ->
            Pid = chatterbox_h2_stream_set:connection(Conn),
            {next_state, ready, Data#data{conn = Conn, conn_pid = Pid}, Actions};
        {error, _} = Error ->
            {next_state, disconnected, Data#data{conn = undefined}, [{reply, From, Error}]}
    end;
connect(Data = #data{conn = Conn, conn_pid = Pid}, From, Actions) when is_pid(Pid) ->
    chatterbox_h2_connection:stop(Conn),
    connect(Data#data{conn = undefined, conn_pid = undefined}, From, Actions).

-spec options(http | https, [term()]) -> [term()].
options(https, Options) -> Options;
options(http, Options) -> Options.
