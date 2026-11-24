-module(vault_calls_chat).
-moduledoc "Production Chat adapter: direct-room membership is checked with the caller token.".

-export([get_room/3, check_membership/2]).

-spec get_room(binary(), binary(), binary()) -> {ok, binary()} | {error, not_found | unavailable}.
get_room(RoomId, User, Token) ->
    vault_calls_metrics:dependency(chat, fun() -> get_room_request(RoomId, User, Token) end).

-spec get_room_request(binary(), binary(), binary()) -> {ok, binary()} | {error, not_found | unavailable}.
get_room_request(RoomId, User, Token) when is_binary(RoomId), is_binary(User), is_binary(Token) ->
    Deadline = ctx:with_deadline_after(ctx:new(), 2000, millisecond),
    Ctx = grpcbox_metadata:append_to_outgoing_ctx(Deadline, #{
        <<"authorization">> => <<"Bearer ", Token/binary>>
    }),
    case chat_v_1_chat_service_client:get_room(Ctx, #{room_id => RoomId}, #{channel => calls_chat}) of
        {ok, #{room := #{kind := 'ROOM_KIND_DIRECT', participant_ids := Participants}}, _Metadata} ->
            other_participant(Participants, User);
        {ok, _, _} -> {error, not_found};
        {error, {<<"5">>, _}, _} -> {error, not_found};
        {error, {<<"16">>, _}, _} -> {error, not_found};
        {error, {<<"7">>, _}, _} -> {error, not_found};
        _ -> {error, unavailable}
    end;
get_room_request(_, _, _) -> {error, not_found}.

-spec other_participant([binary()], binary()) -> {ok, binary()} | {error, not_found}.
other_participant([User, Other], User) when is_binary(Other), Other =/= User -> {ok, Other};
other_participant([Other, User], User) when is_binary(Other), Other =/= User -> {ok, Other};
other_participant(_, _) -> {error, not_found}.

-spec check_membership(binary(), binary()) -> ok | {error, removed | unavailable}.
check_membership(RoomId, User) ->
    vault_calls_metrics:dependency(chat, fun() -> check_membership_request(RoomId, User) end).

-spec check_membership_request(binary(), binary()) -> ok | {error, removed | unavailable}.
check_membership_request(RoomId, User) ->
    Ctx = ctx:with_deadline_after(ctx:new(), 2000, millisecond),
    case chat_v_1_chat_service_client:check_membership(Ctx, #{room_id => RoomId, user_id => User},
                                                       #{channel => calls_chat}) of
        {ok, #{room := _}, _} -> ok;
        {error, {<<"5">>, _}, _} -> {error, removed};
        {error, {<<"3">>, _}, _} -> {error, removed};
        _ -> {error, unavailable}
    end.
