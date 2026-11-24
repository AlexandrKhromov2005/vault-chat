%%%-------------------------------------------------------------------
%% @doc Client module for grpc service chat.v1.ChatService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(chat_v_1_chat_service_client).

-compile(export_all).
-compile(nowarn_export_all).

-include_lib("grpcbox/include/grpcbox.hrl").

-define(is_ctx(Ctx), is_tuple(Ctx) andalso element(1, Ctx) =:= ctx).

-define(SERVICE, 'chat.v1.ChatService').
-define(PROTO_MODULE, 'chat_pb').
-define(MARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:encode_msg(I, T) end).
-define(UNMARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:decode_msg(I, T) end).
-define(DEF(Input, Output, MessageType), #grpcbox_def{service=?SERVICE,
                                                      message_type=MessageType,
                                                      marshal_fun=?MARSHAL_FUN(Input),
                                                      unmarshal_fun=?UNMARSHAL_FUN(Output)}).

%% @doc Unary RPC
-spec get_or_create_direct(chat_pb:get_or_create_direct_request()) ->
    {ok, chat_pb:get_or_create_direct_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_or_create_direct(Input) ->
    get_or_create_direct(ctx:new(), Input, #{}).

-spec get_or_create_direct(ctx:t() | chat_pb:get_or_create_direct_request(), chat_pb:get_or_create_direct_request() | grpcbox_client:options()) ->
    {ok, chat_pb:get_or_create_direct_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_or_create_direct(Ctx, Input) when ?is_ctx(Ctx) ->
    get_or_create_direct(Ctx, Input, #{});
get_or_create_direct(Input, Options) ->
    get_or_create_direct(ctx:new(), Input, Options).

-spec get_or_create_direct(ctx:t(), chat_pb:get_or_create_direct_request(), grpcbox_client:options()) ->
    {ok, chat_pb:get_or_create_direct_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_or_create_direct(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/GetOrCreateDirect">>, Input, ?DEF(get_or_create_direct_request, get_or_create_direct_response, <<"chat.v1.GetOrCreateDirectRequest">>), Options).

%% @doc Unary RPC
-spec create_channel(chat_pb:create_channel_request()) ->
    {ok, chat_pb:create_channel_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create_channel(Input) ->
    create_channel(ctx:new(), Input, #{}).

-spec create_channel(ctx:t() | chat_pb:create_channel_request(), chat_pb:create_channel_request() | grpcbox_client:options()) ->
    {ok, chat_pb:create_channel_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create_channel(Ctx, Input) when ?is_ctx(Ctx) ->
    create_channel(Ctx, Input, #{});
create_channel(Input, Options) ->
    create_channel(ctx:new(), Input, Options).

-spec create_channel(ctx:t(), chat_pb:create_channel_request(), grpcbox_client:options()) ->
    {ok, chat_pb:create_channel_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create_channel(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/CreateChannel">>, Input, ?DEF(create_channel_request, create_channel_response, <<"chat.v1.CreateChannelRequest">>), Options).

%% @doc Unary RPC
-spec get_room(chat_pb:get_room_request()) ->
    {ok, chat_pb:get_room_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_room(Input) ->
    get_room(ctx:new(), Input, #{}).

-spec get_room(ctx:t() | chat_pb:get_room_request(), chat_pb:get_room_request() | grpcbox_client:options()) ->
    {ok, chat_pb:get_room_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_room(Ctx, Input) when ?is_ctx(Ctx) ->
    get_room(Ctx, Input, #{});
get_room(Input, Options) ->
    get_room(ctx:new(), Input, Options).

-spec get_room(ctx:t(), chat_pb:get_room_request(), grpcbox_client:options()) ->
    {ok, chat_pb:get_room_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_room(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/GetRoom">>, Input, ?DEF(get_room_request, get_room_response, <<"chat.v1.GetRoomRequest">>), Options).

%% @doc Unary RPC
-spec check_membership(chat_pb:check_membership_request()) ->
    {ok, chat_pb:check_membership_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
check_membership(Input) ->
    check_membership(ctx:new(), Input, #{}).

-spec check_membership(ctx:t() | chat_pb:check_membership_request(), chat_pb:check_membership_request() | grpcbox_client:options()) ->
    {ok, chat_pb:check_membership_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
check_membership(Ctx, Input) when ?is_ctx(Ctx) ->
    check_membership(Ctx, Input, #{});
check_membership(Input, Options) ->
    check_membership(ctx:new(), Input, Options).

-spec check_membership(ctx:t(), chat_pb:check_membership_request(), grpcbox_client:options()) ->
    {ok, chat_pb:check_membership_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
check_membership(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/CheckMembership">>, Input, ?DEF(check_membership_request, check_membership_response, <<"chat.v1.CheckMembershipRequest">>), Options).

%% @doc Unary RPC
-spec list_rooms(chat_pb:list_rooms_request()) ->
    {ok, chat_pb:list_rooms_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_rooms(Input) ->
    list_rooms(ctx:new(), Input, #{}).

-spec list_rooms(ctx:t() | chat_pb:list_rooms_request(), chat_pb:list_rooms_request() | grpcbox_client:options()) ->
    {ok, chat_pb:list_rooms_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_rooms(Ctx, Input) when ?is_ctx(Ctx) ->
    list_rooms(Ctx, Input, #{});
list_rooms(Input, Options) ->
    list_rooms(ctx:new(), Input, Options).

-spec list_rooms(ctx:t(), chat_pb:list_rooms_request(), grpcbox_client:options()) ->
    {ok, chat_pb:list_rooms_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_rooms(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/ListRooms">>, Input, ?DEF(list_rooms_request, list_rooms_response, <<"chat.v1.ListRoomsRequest">>), Options).

%% @doc Unary RPC
-spec add_member(chat_pb:add_member_request()) ->
    {ok, chat_pb:add_member_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
add_member(Input) ->
    add_member(ctx:new(), Input, #{}).

-spec add_member(ctx:t() | chat_pb:add_member_request(), chat_pb:add_member_request() | grpcbox_client:options()) ->
    {ok, chat_pb:add_member_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
add_member(Ctx, Input) when ?is_ctx(Ctx) ->
    add_member(Ctx, Input, #{});
add_member(Input, Options) ->
    add_member(ctx:new(), Input, Options).

-spec add_member(ctx:t(), chat_pb:add_member_request(), grpcbox_client:options()) ->
    {ok, chat_pb:add_member_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
add_member(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/AddMember">>, Input, ?DEF(add_member_request, add_member_response, <<"chat.v1.AddMemberRequest">>), Options).

%% @doc Unary RPC
-spec list_members(chat_pb:list_members_request()) ->
    {ok, chat_pb:list_members_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_members(Input) ->
    list_members(ctx:new(), Input, #{}).

-spec list_members(ctx:t() | chat_pb:list_members_request(), chat_pb:list_members_request() | grpcbox_client:options()) ->
    {ok, chat_pb:list_members_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_members(Ctx, Input) when ?is_ctx(Ctx) ->
    list_members(Ctx, Input, #{});
list_members(Input, Options) ->
    list_members(ctx:new(), Input, Options).

-spec list_members(ctx:t(), chat_pb:list_members_request(), grpcbox_client:options()) ->
    {ok, chat_pb:list_members_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_members(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/ListMembers">>, Input, ?DEF(list_members_request, list_members_response, <<"chat.v1.ListMembersRequest">>), Options).

%% @doc Unary RPC
-spec remove_member(chat_pb:remove_member_request()) ->
    {ok, chat_pb:remove_member_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
remove_member(Input) ->
    remove_member(ctx:new(), Input, #{}).

-spec remove_member(ctx:t() | chat_pb:remove_member_request(), chat_pb:remove_member_request() | grpcbox_client:options()) ->
    {ok, chat_pb:remove_member_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
remove_member(Ctx, Input) when ?is_ctx(Ctx) ->
    remove_member(Ctx, Input, #{});
remove_member(Input, Options) ->
    remove_member(ctx:new(), Input, Options).

-spec remove_member(ctx:t(), chat_pb:remove_member_request(), grpcbox_client:options()) ->
    {ok, chat_pb:remove_member_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
remove_member(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/RemoveMember">>, Input, ?DEF(remove_member_request, remove_member_response, <<"chat.v1.RemoveMemberRequest">>), Options).

%% @doc Unary RPC
-spec leave_room(chat_pb:leave_room_request()) ->
    {ok, chat_pb:leave_room_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
leave_room(Input) ->
    leave_room(ctx:new(), Input, #{}).

-spec leave_room(ctx:t() | chat_pb:leave_room_request(), chat_pb:leave_room_request() | grpcbox_client:options()) ->
    {ok, chat_pb:leave_room_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
leave_room(Ctx, Input) when ?is_ctx(Ctx) ->
    leave_room(Ctx, Input, #{});
leave_room(Input, Options) ->
    leave_room(ctx:new(), Input, Options).

-spec leave_room(ctx:t(), chat_pb:leave_room_request(), grpcbox_client:options()) ->
    {ok, chat_pb:leave_room_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
leave_room(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/LeaveRoom">>, Input, ?DEF(leave_room_request, leave_room_response, <<"chat.v1.LeaveRoomRequest">>), Options).

%% @doc Unary RPC
-spec send_message(chat_pb:send_message_request()) ->
    {ok, chat_pb:send_message_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
send_message(Input) ->
    send_message(ctx:new(), Input, #{}).

-spec send_message(ctx:t() | chat_pb:send_message_request(), chat_pb:send_message_request() | grpcbox_client:options()) ->
    {ok, chat_pb:send_message_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
send_message(Ctx, Input) when ?is_ctx(Ctx) ->
    send_message(Ctx, Input, #{});
send_message(Input, Options) ->
    send_message(ctx:new(), Input, Options).

-spec send_message(ctx:t(), chat_pb:send_message_request(), grpcbox_client:options()) ->
    {ok, chat_pb:send_message_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
send_message(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/SendMessage">>, Input, ?DEF(send_message_request, send_message_response, <<"chat.v1.SendMessageRequest">>), Options).

%% @doc Unary RPC
-spec list_messages(chat_pb:list_messages_request()) ->
    {ok, chat_pb:list_messages_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_messages(Input) ->
    list_messages(ctx:new(), Input, #{}).

-spec list_messages(ctx:t() | chat_pb:list_messages_request(), chat_pb:list_messages_request() | grpcbox_client:options()) ->
    {ok, chat_pb:list_messages_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_messages(Ctx, Input) when ?is_ctx(Ctx) ->
    list_messages(Ctx, Input, #{});
list_messages(Input, Options) ->
    list_messages(ctx:new(), Input, Options).

-spec list_messages(ctx:t(), chat_pb:list_messages_request(), grpcbox_client:options()) ->
    {ok, chat_pb:list_messages_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
list_messages(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/chat.v1.ChatService/ListMessages">>, Input, ?DEF(list_messages_request, list_messages_response, <<"chat.v1.ListMessagesRequest">>), Options).

