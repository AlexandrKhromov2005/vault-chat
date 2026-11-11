-module(vault_calls_chat_stub).
-moduledoc "Configurable direct-room adapter for the loopback prototype.".

-export([get_room/2]).

-spec get_room(binary(), binary()) -> {ok, binary()} | {error, not_found | unavailable}.
get_room(RoomId, User) ->
    ok = delay(chat_delay_ms),
    case application:get_env(vault_calls, chat_available, true) of
        false -> {error, unavailable};
        true -> member(RoomId, User)
    end.

-spec member(binary(), binary()) -> {ok, binary()} | {error, not_found}.
member(RoomId, User) ->
    Rooms = application:get_env(vault_calls, rooms, #{}),
    case maps:get(RoomId, Rooms, undefined) of
        [User, Other] -> {ok, Other};
        [Other, User] -> {ok, Other};
        _ -> {error, not_found}
    end.

-spec delay(atom()) -> ok.
delay(Name) ->
    Milliseconds = application:get_env(vault_calls, Name, 0),
    timer:sleep(min(Milliseconds, 2000)),
    ok.
