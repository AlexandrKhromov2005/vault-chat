%%%-------------------------------------------------------------------
%% @doc Behaviour to implement for grpc service chat.v1.ChatService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(chat_v_1_chat_service_bhvr).

%% Unary RPC
-callback get_or_create_direct(ctx:t(), chat_pb:get_or_create_direct_request()) ->
    {ok, chat_pb:get_or_create_direct_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback create_channel(ctx:t(), chat_pb:create_channel_request()) ->
    {ok, chat_pb:create_channel_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback get_room(ctx:t(), chat_pb:get_room_request()) ->
    {ok, chat_pb:get_room_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback check_membership(ctx:t(), chat_pb:check_membership_request()) ->
    {ok, chat_pb:check_membership_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback list_rooms(ctx:t(), chat_pb:list_rooms_request()) ->
    {ok, chat_pb:list_rooms_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback add_member(ctx:t(), chat_pb:add_member_request()) ->
    {ok, chat_pb:add_member_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback list_members(ctx:t(), chat_pb:list_members_request()) ->
    {ok, chat_pb:list_members_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback remove_member(ctx:t(), chat_pb:remove_member_request()) ->
    {ok, chat_pb:remove_member_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback leave_room(ctx:t(), chat_pb:leave_room_request()) ->
    {ok, chat_pb:leave_room_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback send_message(ctx:t(), chat_pb:send_message_request()) ->
    {ok, chat_pb:send_message_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback list_messages(ctx:t(), chat_pb:list_messages_request()) ->
    {ok, chat_pb:list_messages_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

