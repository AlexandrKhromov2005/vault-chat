%%%-------------------------------------------------------------------
%% @doc Behaviour to implement for grpc service calls.v1.CallsService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(calls_v_1_calls_service_bhvr).

%% Unary RPC
-callback create(ctx:t(), calls_pb:create_request()) ->
    {ok, calls_pb:create_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback accept(ctx:t(), calls_pb:accept_request()) ->
    {ok, calls_pb:accept_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback reject(ctx:t(), calls_pb:reject_request()) ->
    {ok, calls_pb:reject_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback cancel(ctx:t(), calls_pb:cancel_request()) ->
    {ok, calls_pb:cancel_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback connected(ctx:t(), calls_pb:connected_request()) ->
    {ok, calls_pb:connected_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback hangup(ctx:t(), calls_pb:hangup_request()) ->
    {ok, calls_pb:hangup_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback heartbeat(ctx:t(), calls_pb:heartbeat_request()) ->
    {ok, calls_pb:heartbeat_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback signal(ctx:t(), calls_pb:signal_request()) ->
    {ok, calls_pb:signal_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback get(ctx:t(), calls_pb:get_request()) ->
    {ok, calls_pb:get_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback list_active(ctx:t(), calls_pb:list_active_request()) ->
    {ok, calls_pb:list_active_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback signals(ctx:t(), calls_pb:signals_request()) ->
    {ok, calls_pb:signals_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback turn(ctx:t(), calls_pb:turn_request()) ->
    {ok, calls_pb:turn_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

