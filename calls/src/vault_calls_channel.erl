-module(vault_calls_channel).
-moduledoc "grpcbox channel using the Calls TLS-compatible subchannel.".
-behaviour(gen_statem).

-export([start_link/3, is_ready/1, pick/2, stop/1, stop/2]).
-export([init/1, callback_mode/0, terminate/3, connected/3, idle/3]).

-include_lib("grpcbox/include/grpcbox.hrl").

-define(CHANNEL(Name), {via, gproc, {n, l, {?MODULE, Name}}}).

-type t() :: any().
-type name() :: t().
-type transport() :: http | https.
-type host() :: inet:ip_address() | inet:hostname().
-type connection_settings() :: map().
-type endpoint() :: {transport(), host(), inet:port_number(), [term()]} |
                    {transport(), host(), inet:port_number(), [term()], connection_settings()}.
-type options() :: #{balancer => load_balancer(), encoding => grpcbox:encoding(),
                     unary_interceptor => grpcbox_client:unary_interceptor(),
                     stream_interceptor => grpcbox_client:stream_interceptor(),
                     stats_handler => module(), sync_start => boolean()}.
-type load_balancer() :: round_robin | random | hash | direct | claim.
-export_type([t/0, name/0, options/0, endpoint/0]).

-record(data, {endpoints :: [endpoint()], pool :: atom(), balancer = round_robin :: load_balancer(),
               encoding :: grpcbox:encoding(), stats_handler :: module() | undefined}).

-spec start_link(name(), [endpoint()], options()) -> gen_statem:start_ret().
start_link(Name, Endpoints, Options) ->
    gen_statem:start_link(?CHANNEL(Name), ?MODULE, [Name, Endpoints, Options], []).

-spec is_ready(name()) -> boolean().
is_ready(Name) -> gen_statem:call(?CHANNEL(Name), is_ready).

-spec pick(name(), unary | stream) ->
    {ok, {pid(), grpcbox_client:interceptor() | undefined}} | {error, undefined_channel | no_endpoints}.
pick(Name, CallType) ->
    try
        case gproc_pool:pick_worker(Name) of
            false -> {error, no_endpoints};
            Pid when is_pid(Pid) -> {ok, {Pid, interceptor(Name, CallType)}}
        end
    catch error:badarg -> {error, undefined_channel}
    end.

-spec interceptor(name(), unary | stream) -> grpcbox_client:interceptor() | undefined.
interceptor(Name, CallType) ->
    case ets:lookup(?CHANNELS_TAB, {Name, CallType}) of
        [] -> undefined;
        [{_, Interceptor}] -> Interceptor
    end.

-spec stop(name()) -> ok.
stop(Name) -> stop(Name, {shutdown, force_delete}).

-spec stop(name(), term()) -> ok.
stop(Name, Reason) -> gen_statem:stop(?CHANNEL(Name), Reason, infinity).

-spec init(list()) -> gen_statem:init_result(idle | connected).
init([Name, Endpoints, Options]) ->
    process_flag(trap_exit, true),
    Endpoints1 = normalize_endpoints(Endpoints),
    BalancerType = maps:get(balancer, Options, round_robin),
    Encoding = maps:get(encoding, Options, identity),
    StatsHandler = maps:get(stats_handler, Options, undefined),
    insert_interceptors(Name, Options),
    gproc_pool:new(Name, BalancerType, [{size, length(Endpoints)}, {auto_size, true}]),
    Data = #data{pool = Name, encoding = Encoding, stats_handler = StatsHandler, endpoints = Endpoints1},
    case maps:get(sync_start, Options, false) of
        false -> {ok, idle, Data, [{next_event, internal, connect}]};
        true ->
            _ = start_workers(Name, StatsHandler, Encoding, Endpoints1),
            {ok, connected, Data}
    end.

-spec callback_mode() -> state_functions.
callback_mode() -> state_functions.

-spec connected(gen_statem:event_type(), term(), #data{}) -> gen_statem:event_handler_result(connected).
connected({call, From}, is_ready, _Data) -> {keep_state_and_data, [{reply, From, true}]};
connected(EventType, EventContent, Data) -> handle_event(EventType, EventContent, Data).

-spec idle(gen_statem:event_type(), term(), #data{}) -> gen_statem:event_handler_result(idle | connected).
idle(internal, connect, Data = #data{pool = Pool, stats_handler = StatsHandler,
                                    encoding = Encoding, endpoints = Endpoints}) ->
    _ = start_workers(Pool, StatsHandler, Encoding, Endpoints),
    {next_state, connected, Data};
idle({call, From}, is_ready, _Data) -> {keep_state_and_data, [{reply, From, false}]};
idle(EventType, EventContent, Data) -> handle_event(EventType, EventContent, Data).

-spec handle_event(gen_statem:event_type(), term(), #data{}) -> gen_statem:event_handler_result(atom()).
handle_event(_, _, Data) -> {keep_state, Data}.

-spec terminate(term(), atom(), #data{}) -> ok | true.
terminate({shutdown, force_delete}, _State, #data{pool = Name}) -> gproc_pool:force_delete(Name);
terminate(Reason, _State, #data{pool = Name}) ->
    [vault_calls_subchannel:stop(Pid, Reason) || {_Channel, Pid} <- gproc_pool:active_workers(Name)],
    gproc_pool:delete(Name),
    ok.

-spec insert_interceptors(name(), options()) -> ok | true.
insert_interceptors(Name, Interceptors) ->
    insert_unary_interceptor(Name, Interceptors),
    insert_stream_interceptor(Name, Interceptors).

-spec insert_unary_interceptor(name(), options()) -> ok | true.
insert_unary_interceptor(Name, Interceptors) ->
    case maps:get(unary_interceptor, Interceptors, undefined) of
        undefined -> ok;
        {Interceptor, Arg} -> ets:insert(?CHANNELS_TAB, {{Name, unary}, Interceptor(Arg)});
        Interceptor -> ets:insert(?CHANNELS_TAB, {{Name, unary}, Interceptor})
    end.

-spec insert_stream_interceptor(name(), options()) -> ok | true.
insert_stream_interceptor(Name, Interceptors) ->
    case maps:get(stream_interceptor, Interceptors, undefined) of
        undefined -> ok;
        {Interceptor, Arg} -> ets:insert(?CHANNELS_TAB, {{Name, stream}, Interceptor(Arg)});
        Interceptor when is_atom(Interceptor) ->
            ets:insert(?CHANNELS_TAB, {{Name, stream}, #{new_stream => fun Interceptor:new_stream/6,
                                                         send_msg => fun Interceptor:send_msg/3,
                                                         recv_msg => fun Interceptor:recv_msg/3}});
        Interceptor = #{new_stream := _, send_msg := _, recv_msg := _} ->
            ets:insert(?CHANNELS_TAB, {{Name, stream}, Interceptor})
    end.

-spec start_workers(atom(), module() | undefined, grpcbox:encoding(), [endpoint()]) -> [pid()].
start_workers(Pool, StatsHandler, Encoding, Endpoints) ->
    [begin
         gproc_pool:add_worker(Pool, Endpoint),
         {ok, Pid} = vault_calls_subchannel:start_link(Endpoint, Pool,
             {Transport, Host, Port, SSLOptions, ConnectionSettings}, Encoding, StatsHandler),
         Pid
     end || Endpoint = {Transport, Host, Port, SSLOptions, ConnectionSettings} <- Endpoints].

-spec normalize_endpoints([endpoint()]) -> [endpoint()].
normalize_endpoints(Endpoints) ->
    lists:map(fun({Transport, Host, Port, SSLOptions}) ->
                      {Transport, Host, Port, SSLOptions, #{}};
                 ({Transport, Host, Port, SSLOptions, ConnectionSettings}) ->
                      {Transport, Host, Port, SSLOptions, ConnectionSettings}
              end, Endpoints).
