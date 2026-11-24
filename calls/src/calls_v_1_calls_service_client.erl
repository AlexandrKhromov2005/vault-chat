%%%-------------------------------------------------------------------
%% @doc Client module for grpc service calls.v1.CallsService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(calls_v_1_calls_service_client).

-compile(export_all).
-compile(nowarn_export_all).

-include_lib("grpcbox/include/grpcbox.hrl").

-define(is_ctx(Ctx), is_tuple(Ctx) andalso element(1, Ctx) =:= ctx).

-define(SERVICE, 'calls.v1.CallsService').
-define(PROTO_MODULE, 'calls_pb').
-define(MARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:encode_msg(I, T) end).
-define(UNMARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:decode_msg(I, T) end).
-define(DEF(Input, Output, MessageType), #grpcbox_def{service=?SERVICE,
                                                      message_type=MessageType,
                                                      marshal_fun=?MARSHAL_FUN(Input),
                                                      unmarshal_fun=?UNMARSHAL_FUN(Output)}).

%% @doc Unary RPC
-spec create(calls_pb:create_request()) ->
    {ok, calls_pb:create_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create(Input) ->
    create(ctx:new(), Input, #{}).

-spec create(ctx:t() | calls_pb:create_request(), calls_pb:create_request() | grpcbox_client:options()) ->
    {ok, calls_pb:create_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create(Ctx, Input) when ?is_ctx(Ctx) ->
    create(Ctx, Input, #{});
create(Input, Options) ->
    create(ctx:new(), Input, Options).

-spec create(ctx:t(), calls_pb:create_request(), grpcbox_client:options()) ->
    {ok, calls_pb:create_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Create">>, Input, ?DEF(create_request, create_response, <<"calls.v1.CreateRequest">>), Options).

%% @doc Unary RPC
-spec accept(calls_pb:accept_request()) ->
    {ok, calls_pb:accept_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
accept(Input) ->
    accept(ctx:new(), Input, #{}).

-spec accept(ctx:t() | calls_pb:accept_request(), calls_pb:accept_request() | grpcbox_client:options()) ->
    {ok, calls_pb:accept_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
accept(Ctx, Input) when ?is_ctx(Ctx) ->
    accept(Ctx, Input, #{});
accept(Input, Options) ->
    accept(ctx:new(), Input, Options).

-spec accept(ctx:t(), calls_pb:accept_request(), grpcbox_client:options()) ->
    {ok, calls_pb:accept_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
accept(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Accept">>, Input, ?DEF(accept_request, accept_response, <<"calls.v1.AcceptRequest">>), Options).

%% @doc Unary RPC
-spec reject(calls_pb:reject_request()) ->
    {ok, calls_pb:reject_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
reject(Input) ->
    reject(ctx:new(), Input, #{}).

-spec reject(ctx:t() | calls_pb:reject_request(), calls_pb:reject_request() | grpcbox_client:options()) ->
    {ok, calls_pb:reject_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
reject(Ctx, Input) when ?is_ctx(Ctx) ->
    reject(Ctx, Input, #{});
reject(Input, Options) ->
    reject(ctx:new(), Input, Options).

-spec reject(ctx:t(), calls_pb:reject_request(), grpcbox_client:options()) ->
    {ok, calls_pb:reject_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
reject(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Reject">>, Input, ?DEF(reject_request, reject_response, <<"calls.v1.RejectRequest">>), Options).

%% @doc Unary RPC
-spec cancel(calls_pb:cancel_request()) ->
    {ok, calls_pb:cancel_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
cancel(Input) ->
    cancel(ctx:new(), Input, #{}).

-spec cancel(ctx:t() | calls_pb:cancel_request(), calls_pb:cancel_request() | grpcbox_client:options()) ->
    {ok, calls_pb:cancel_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
cancel(Ctx, Input) when ?is_ctx(Ctx) ->
    cancel(Ctx, Input, #{});
cancel(Input, Options) ->
    cancel(ctx:new(), Input, Options).

-spec cancel(ctx:t(), calls_pb:cancel_request(), grpcbox_client:options()) ->
    {ok, calls_pb:cancel_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
cancel(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Cancel">>, Input, ?DEF(cancel_request, cancel_response, <<"calls.v1.CancelRequest">>), Options).

%% @doc Unary RPC
-spec connected(calls_pb:connected_request()) ->
    {ok, calls_pb:connected_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
connected(Input) ->
    connected(ctx:new(), Input, #{}).

-spec connected(ctx:t() | calls_pb:connected_request(), calls_pb:connected_request() | grpcbox_client:options()) ->
    {ok, calls_pb:connected_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
connected(Ctx, Input) when ?is_ctx(Ctx) ->
    connected(Ctx, Input, #{});
connected(Input, Options) ->
    connected(ctx:new(), Input, Options).

-spec connected(ctx:t(), calls_pb:connected_request(), grpcbox_client:options()) ->
    {ok, calls_pb:connected_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
connected(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Connected">>, Input, ?DEF(connected_request, connected_response, <<"calls.v1.ConnectedRequest">>), Options).

%% @doc Unary RPC
-spec hangup(calls_pb:hangup_request()) ->
    {ok, calls_pb:hangup_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
hangup(Input) ->
    hangup(ctx:new(), Input, #{}).

-spec hangup(ctx:t() | calls_pb:hangup_request(), calls_pb:hangup_request() | grpcbox_client:options()) ->
    {ok, calls_pb:hangup_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
hangup(Ctx, Input) when ?is_ctx(Ctx) ->
    hangup(Ctx, Input, #{});
hangup(Input, Options) ->
    hangup(ctx:new(), Input, Options).

-spec hangup(ctx:t(), calls_pb:hangup_request(), grpcbox_client:options()) ->
    {ok, calls_pb:hangup_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
hangup(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Hangup">>, Input, ?DEF(hangup_request, hangup_response, <<"calls.v1.HangupRequest">>), Options).

%% @doc Unary RPC
-spec heartbeat(calls_pb:heartbeat_request()) ->
    {ok, calls_pb:heartbeat_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
heartbeat(Input) ->
    heartbeat(ctx:new(), Input, #{}).

-spec heartbeat(ctx:t() | calls_pb:heartbeat_request(), calls_pb:heartbeat_request() | grpcbox_client:options()) ->
    {ok, calls_pb:heartbeat_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
heartbeat(Ctx, Input) when ?is_ctx(Ctx) ->
    heartbeat(Ctx, Input, #{});
heartbeat(Input, Options) ->
    heartbeat(ctx:new(), Input, Options).

-spec heartbeat(ctx:t(), calls_pb:heartbeat_request(), grpcbox_client:options()) ->
    {ok, calls_pb:heartbeat_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
heartbeat(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Heartbeat">>, Input, ?DEF(heartbeat_request, heartbeat_response, <<"calls.v1.HeartbeatRequest">>), Options).

%% @doc Unary RPC
-spec signal(calls_pb:signal_request()) ->
    {ok, calls_pb:signal_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
signal(Input) ->
    signal(ctx:new(), Input, #{}).

-spec signal(ctx:t() | calls_pb:signal_request(), calls_pb:signal_request() | grpcbox_client:options()) ->
    {ok, calls_pb:signal_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
signal(Ctx, Input) when ?is_ctx(Ctx) ->
    signal(Ctx, Input, #{});
signal(Input, Options) ->
    signal(ctx:new(), Input, Options).

-spec signal(ctx:t(), calls_pb:signal_request(), grpcbox_client:options()) ->
    {ok, calls_pb:signal_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
signal(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Signal">>, Input, ?DEF(signal_request, signal_response, <<"calls.v1.SignalRequest">>), Options).

%% @doc Unary RPC
-spec get(calls_pb:get_request()) ->
    {ok, calls_pb:get_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get(Input) ->
    get(ctx:new(), Input, #{}).

-spec get(ctx:t() | calls_pb:get_request(), calls_pb:get_request() | grpcbox_client:options()) ->
    {ok, calls_pb:get_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get(Ctx, Input) when ?is_ctx(Ctx) ->
    get(Ctx, Input, #{});
get(Input, Options) ->
    get(ctx:new(), Input, Options).

-spec get(ctx:t(), calls_pb:get_request(), grpcbox_client:options()) ->
    {ok, calls_pb:get_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Get">>, Input, ?DEF(get_request, get_response, <<"calls.v1.GetRequest">>), Options).

%% @doc Unary RPC
-spec list_active(calls_pb:list_active_request()) ->
    {ok, calls_pb:list_active_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_active(Input) ->
    list_active(ctx:new(), Input, #{}).

-spec list_active(ctx:t() | calls_pb:list_active_request(), calls_pb:list_active_request() | grpcbox_client:options()) ->
    {ok, calls_pb:list_active_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_active(Ctx, Input) when ?is_ctx(Ctx) ->
    list_active(Ctx, Input, #{});
list_active(Input, Options) ->
    list_active(ctx:new(), Input, Options).

-spec list_active(ctx:t(), calls_pb:list_active_request(), grpcbox_client:options()) ->
    {ok, calls_pb:list_active_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_active(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/ListActive">>, Input, ?DEF(list_active_request, list_active_response, <<"calls.v1.ListActiveRequest">>), Options).

%% @doc Unary RPC
-spec signals(calls_pb:signals_request()) ->
    {ok, calls_pb:signals_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
signals(Input) ->
    signals(ctx:new(), Input, #{}).

-spec signals(ctx:t() | calls_pb:signals_request(), calls_pb:signals_request() | grpcbox_client:options()) ->
    {ok, calls_pb:signals_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
signals(Ctx, Input) when ?is_ctx(Ctx) ->
    signals(Ctx, Input, #{});
signals(Input, Options) ->
    signals(ctx:new(), Input, Options).

-spec signals(ctx:t(), calls_pb:signals_request(), grpcbox_client:options()) ->
    {ok, calls_pb:signals_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
signals(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Signals">>, Input, ?DEF(signals_request, signals_response, <<"calls.v1.SignalsRequest">>), Options).

%% @doc Unary RPC
-spec turn(calls_pb:turn_request()) ->
    {ok, calls_pb:turn_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
turn(Input) ->
    turn(ctx:new(), Input, #{}).

-spec turn(ctx:t() | calls_pb:turn_request(), calls_pb:turn_request() | grpcbox_client:options()) ->
    {ok, calls_pb:turn_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
turn(Ctx, Input) when ?is_ctx(Ctx) ->
    turn(Ctx, Input, #{});
turn(Input, Options) ->
    turn(ctx:new(), Input, Options).

-spec turn(ctx:t(), calls_pb:turn_request(), grpcbox_client:options()) ->
    {ok, calls_pb:turn_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
turn(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/calls.v1.CallsService/Turn">>, Input, ?DEF(turn_request, turn_response, <<"calls.v1.TurnRequest">>), Options).

